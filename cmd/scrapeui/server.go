package scrapeui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/flanksource/commons/har"
	v1 "github.com/flanksource/config-db/api/v1"
)

type Server struct {
	mu         sync.RWMutex
	scrapers   []ScraperProgress
	results    v1.FullScrapeResults
	summary    *SaveSummary
	har        []har.Entry
	scrapeSpec any
	logBuf     *bytes.Buffer
	done       bool
	startedAt  int64
	updated    chan struct{}
}

func NewServer(scraperNames []string, scrapeSpec any, logBuf *bytes.Buffer) *Server {
	scrapers := make([]ScraperProgress, len(scraperNames))
	for i, name := range scraperNames {
		scrapers[i] = ScraperProgress{
			Name:   ScraperName(name),
			Status: ScraperPending,
		}
	}
	return &Server{
		scrapers:   scrapers,
		scrapeSpec: scrapeSpec,
		logBuf:     logBuf,
		updated:    make(chan struct{}, 1),
		startedAt:  time.Now().UnixMilli(),
	}
}

func (s *Server) UpdateScraper(name string, status ScraperStatus, results []v1.ScrapeResult, summary *v1.ScrapeSummary, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	displayName := ScraperName(name)
	for i := range s.scrapers {
		if s.scrapers[i].Name != displayName {
			continue
		}
		s.scrapers[i].Status = status
		if status == ScraperRunning {
			now := time.Now()
			s.scrapers[i].StartedAt = &now
		}
		if status == ScraperComplete || status == ScraperError {
			if s.scrapers[i].StartedAt != nil {
				s.scrapers[i].DurationSec = time.Since(*s.scrapers[i].StartedAt).Seconds()
			}
		}
		if err != nil {
			s.scrapers[i].Error = err.Error()
		}
		if results != nil {
			merged := MergeResults(results)
			s.scrapers[i].ResultCount = len(merged.Configs)
			s.results.Configs = append(s.results.Configs, merged.Configs...)
			s.results.Changes = append(s.results.Changes, merged.Changes...)
			s.results.Analysis = append(s.results.Analysis, merged.Analysis...)
			s.results.Relationships = append(s.results.Relationships, merged.Relationships...)
			s.results.ExternalUsers = append(s.results.ExternalUsers, merged.ExternalUsers...)
			s.results.ExternalGroups = append(s.results.ExternalGroups, merged.ExternalGroups...)
			s.results.ExternalRoles = append(s.results.ExternalRoles, merged.ExternalRoles...)
			s.results.ExternalUserGroups = append(s.results.ExternalUserGroups, merged.ExternalUserGroups...)
			s.results.ConfigAccess = append(s.results.ConfigAccess, merged.ConfigAccess...)
			s.results.ConfigAccessLogs = append(s.results.ConfigAccessLogs, merged.ConfigAccessLogs...)
		}
		if summary != nil {
			s.summary = ConvertSaveSummary(summary)
		}
		break
	}
	s.notify()
}

func (s *Server) SetHAR(entries []har.Entry) {
	s.mu.Lock()
	s.har = entries
	s.mu.Unlock()
	s.notify()
}

func NewStaticServer(snap Snapshot) *Server {
	snap.Done = true
	snap.Counts = BuildCounts(snap.Results)
	if snap.StartedAt == 0 {
		snap.StartedAt = time.Now().UnixMilli()
	}
	return &Server{
		scrapers:  snap.Scrapers,
		results:   snap.Results,
		har:       snap.HAR,
		done:      true,
		startedAt: snap.StartedAt,
		updated:   make(chan struct{}, 1),
	}
}

func (s *Server) SetDone() {
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
	s.notify()
}

func (s *Server) notify() {
	select {
	case s.updated <- struct{}{}:
	default:
	}
}

func (s *Server) snapshot() Snapshot {
	logs := ""
	if s.logBuf != nil {
		logs = s.logBuf.String()
	}
	return Snapshot{
		Scrapers:    s.scrapers,
		Results:     s.results,
		Counts:      BuildCounts(s.results),
		SaveSummary: s.summary,
		ScrapeSpec:  s.scrapeSpec,
		HAR:         s.har,
		Logs:        logs,
		Done:        s.done,
		StartedAt:   s.startedAt,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/api/scrape", s.handleJSON)
	mux.HandleFunc("/api/scrape/stream", s.handleSSE)
	return mux
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, pageHTML())
}

func pageHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Scrape Results</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script src="https://code.iconify.design/iconify-icon/2.0.0/iconify-icon.min.js"></script>
</head>
<body>
    <div id="root"></div>
    <script>` + bundleJS + `</script>
</body>
</html>`
}

func (s *Server) handleJSON(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	data := s.snapshot()
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data) //nolint:errcheck
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	s.mu.RLock()
	initial := s.snapshot()
	s.mu.RUnlock()
	if b, err := json.Marshal(initial); err == nil {
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.updated:
		case <-ticker.C:
		}

		s.mu.RLock()
		data := s.snapshot()
		s.mu.RUnlock()

		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()

		if data.Done {
			fmt.Fprintf(w, "event: done\ndata: {}\n\n")
			flusher.Flush()
			return
		}
	}
}
