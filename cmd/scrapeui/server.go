package scrapeui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/commons/har"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
)

// mergeEntitiesByID returns a new slice containing one entry per unique ID,
// preferring entries from `incoming` over entries already in `existing` when
// the same ID appears in both (the SaveResults-produced entity is always
// newer). IDs with a zero UUID are treated as unique.
func mergeEntitiesByID[T any](existing, incoming []T, getID func(T) uuid.UUID) []T {
	if len(incoming) == 0 {
		return existing
	}
	out := make([]T, 0, len(existing)+len(incoming))
	byID := make(map[uuid.UUID]int, len(existing)+len(incoming))
	for _, e := range existing {
		id := getID(e)
		if id == uuid.Nil {
			out = append(out, e)
			continue
		}
		byID[id] = len(out)
		out = append(out, e)
	}
	for _, e := range incoming {
		id := getID(e)
		if id == uuid.Nil {
			out = append(out, e)
			continue
		}
		if idx, ok := byID[id]; ok {
			out[idx] = e
			continue
		}
		byID[id] = len(out)
		out = append(out, e)
	}
	return out
}

type Server struct {
	mu            sync.RWMutex
	scrapers      []ScraperProgress
	results       v1.FullScrapeResults
	relationships []UIRelationship
	configMeta    map[string]ConfigMeta
	issues        []ScrapeIssue
	summary       *SaveSummary
	snapshots     map[string]*v1.ScrapeSnapshotPair
	har           []har.Entry
	scrapeSpec    any
	properties    map[string]PropertyInfo
	logLevel      *LogLevelInfo
	logBuf             *bytes.Buffer
	done               bool
	startedAt          int64
	buildInfo          *BuildInfo
	lastScrapeSummary  *v1.ScrapeSummary
	updated            chan struct{}
}

// SetBuildInfo stores the build-time version/commit/date so the frontend can
// display it. Called once at startup by cmd/run.go after constructing the
// server. Kept on Server rather than passed to NewServer to avoid adding yet
// another constructor argument.
func (s *Server) SetBuildInfo(info BuildInfo) {
	s.mu.Lock()
	s.buildInfo = &info
	s.mu.Unlock()
}

func (s *Server) SetLastScrapeSummary(summary v1.ScrapeSummary) {
	s.mu.Lock()
	s.lastScrapeSummary = &summary
	s.mu.Unlock()
}

func (s *Server) SetProperties(props map[string]PropertyInfo, logLevel LogLevelInfo) {
	s.mu.Lock()
	s.properties = props
	s.logLevel = &logLevel
	s.mu.Unlock()
	s.notify()
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
			s.relationships = append(s.relationships, BuildUIRelationships(results)...)
			for k, v := range BuildConfigMeta(results, s.relationships) {
				if s.configMeta == nil {
					s.configMeta = map[string]ConfigMeta{}
				}
				s.configMeta[k] = v
			}
			merged := MergeResults(results)
			s.scrapers[i].ResultCount = len(merged.Configs)
			s.results.Configs = append(s.results.Configs, merged.Configs...)
			s.results.Changes = append(s.results.Changes, merged.Changes...)
			s.results.Analysis = append(s.results.Analysis, merged.Analysis...)
			s.results.Relationships = append(s.results.Relationships, merged.Relationships...)
			s.results.ExternalUserGroups = append(s.results.ExternalUserGroups, merged.ExternalUserGroups...)
			s.results.ConfigAccess = append(s.results.ConfigAccess, merged.ConfigAccess...)
			s.results.ConfigAccessLogs = append(s.results.ConfigAccessLogs, merged.ConfigAccessLogs...)

			// External users/groups/roles: prefer the canonical post-merge
			// entities from the summary (AAD-supplied winner IDs), but also
			// merge in the raw scraper output so results are visible even
			// when summary.Entities is empty — e.g. when SaveResults ran but
			// the SQL merge short-circuited without repopulating Entities,
			// or when running with --no-save.
			s.results.ExternalUsers = mergeEntitiesByID(s.results.ExternalUsers, merged.ExternalUsers, func(u models.ExternalUser) uuid.UUID { return u.ID })
			s.results.ExternalGroups = mergeEntitiesByID(s.results.ExternalGroups, merged.ExternalGroups, func(g models.ExternalGroup) uuid.UUID { return g.ID })
			s.results.ExternalRoles = mergeEntitiesByID(s.results.ExternalRoles, merged.ExternalRoles, func(r models.ExternalRole) uuid.UUID { return r.ID })
			if summary != nil {
				s.results.ExternalUsers = mergeEntitiesByID(s.results.ExternalUsers, summary.ExternalUsers.Entities, func(u models.ExternalUser) uuid.UUID { return u.ID })
				s.results.ExternalGroups = mergeEntitiesByID(s.results.ExternalGroups, summary.ExternalGroups.Entities, func(g models.ExternalGroup) uuid.UUID { return g.ID })
				s.results.ExternalRoles = mergeEntitiesByID(s.results.ExternalRoles, summary.ExternalRoles.Entities, func(r models.ExternalRole) uuid.UUID { return r.ID })
			}
		}
		if summary != nil {
			s.summary = ConvertSaveSummary(summary)
			for i := range summary.OrphanedChanges {
				s.issues = append(s.issues, ScrapeIssue{Type: "orphaned", Message: "Change has no matching config", Change: &summary.OrphanedChanges[i]})
			}
			for i := range summary.FKErrorChanges {
				s.issues = append(s.issues, ScrapeIssue{Type: "fk_error", Message: "Foreign key constraint violation", Change: &summary.FKErrorChanges[i]})
			}
			for i := range summary.Warnings {
				s.issues = append(s.issues, ScrapeIssue{Type: "warning", Message: summary.Warnings[i].Error, Warning: &summary.Warnings[i]})
			}
			for configType, cs := range summary.ConfigTypes {
				for _, w := range cs.Warnings {
					s.issues = append(s.issues, ScrapeIssue{Type: "warning", Message: fmt.Sprintf("[%s] %s", configType, w)})
				}
			}
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

// SetSnapshots records the before/after/diff snapshot pair captured by the
// scrape run for the given scraper. Keyed by scraper name so multi-scraper
// runs keep each pair distinct.
func (s *Server) SetSnapshots(scraperName string, pair *v1.ScrapeSnapshotPair) {
	if pair == nil {
		return
	}
	s.mu.Lock()
	if s.snapshots == nil {
		s.snapshots = map[string]*v1.ScrapeSnapshotPair{}
	}
	s.snapshots[ScraperName(scraperName)] = pair
	s.mu.Unlock()
	s.notify()
}

func NewStaticServer(snap Snapshot) *Server {
	snap.Done = true
	uiRels := snap.Relationships
	if len(uiRels) == 0 {
		uiRels = BuildUIRelationshipsFromDB(snap.Results.Relationships, snap.Results.Configs)
	}
	configMeta := snap.ConfigMeta
	if len(configMeta) == 0 && len(uiRels) > 0 {
		configMeta = BuildConfigMetaFromRelationships(uiRels)
	}
	snap.Counts = BuildCounts(snap.Results, uiRels)
	if snap.StartedAt == 0 {
		snap.StartedAt = time.Now().UnixMilli()
	}
	return &Server{
		scrapers:      snap.Scrapers,
		results:       snap.Results,
		relationships: uiRels,
		configMeta:    configMeta,
		har:           snap.HAR,
		done:          true,
		startedAt:     snap.StartedAt,
		updated:       make(chan struct{}, 1),
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
		Scrapers:      s.scrapers,
		Results:       s.results,
		Relationships: s.relationships,
		ConfigMeta:    s.configMeta,
		Issues:        s.issues,
		Counts:        BuildCounts(s.results, s.relationships),
		SaveSummary:   s.summary,
		Snapshots:     s.snapshots,
		ScrapeSpec:    s.scrapeSpec,
		Properties:    s.properties,
		LogLevel:      s.logLevel,
		HAR:           s.har,
		Logs:          logs,
		Done:          s.done,
		StartedAt:     s.startedAt,
		BuildInfo:          s.buildInfo,
		LastScrapeSummary:  s.lastScrapeSummary,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/api/scrape", s.handleJSON)
	mux.HandleFunc("/api/scrape/stream", s.handleSSE)
	mux.HandleFunc("/api/config/", s.handleConfigItem)
	return mux
}

func (s *Server) handleConfigItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/config/")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var item *v1.ScrapeResult
	for i := range s.results.Configs {
		if s.results.Configs[i].ID == id {
			item = &s.results.Configs[i]
			break
		}
	}
	if item == nil {
		http.NotFound(w, r)
		return
	}

	type configItemDetail struct {
		v1.ScrapeResult
		Meta          *ConfigMeta      `json:"_meta,omitempty"`
		Relationships []UIRelationship `json:"_relationships,omitempty"`
		Changes       []v1.ChangeResult `json:"_changes,omitempty"`
	}

	detail := configItemDetail{ScrapeResult: *item}
	if meta, ok := s.configMeta[id]; ok {
		detail.Meta = &meta
	}
	for _, rel := range s.relationships {
		if rel.ConfigExternalID == id || rel.RelatedExternalID == id {
			detail.Relationships = append(detail.Relationships, rel)
		}
	}
	for _, ch := range s.results.Changes {
		if ch.Source != "" && strings.Contains(ch.Source, id) {
			detail.Changes = append(detail.Changes, ch)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, sanitizeFilename(id)))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(detail) //nolint:errcheck
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// SPA routes that the Preact app handles client-side. Any request for one of
// these prefixes (or the bare root) should return the HTML shell so that
// deep links like /configs/{id} or /groups/{id} work on refresh.
var spaRoutes = []string{
	"/configs", "/logs", "/har", "/users", "/groups",
	"/roles", "/access", "/access_logs", "/issues", "/snapshot", "/last_summary", "/spec",
}

func isSPARoute(path string) bool {
	if path == "/" {
		return true
	}
	for _, prefix := range spaRoutes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if !isSPARoute(r.URL.Path) {
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
