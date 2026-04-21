package scrapers

import (
	gocontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	dutyAPI "github.com/flanksource/duty/api"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
)

const (
	runNowTimeout         = 30 * time.Minute
	runNowRequestMaxBytes = 8 * 1024
)

type runNowResponse struct {
	JobHistoryID  string `json:"job_history_id,omitempty"`
	RunArtifactID string `json:"run_artifact_id,omitempty"`
	Status        string `json:"status,omitempty"`
}

type runNowRequest struct {
	Async                 *bool `json:"async,omitempty"`
	CaptureHAR            *bool `json:"capture_har,omitempty"`
	CaptureHARCamel       *bool `json:"captureHAR,omitempty"`
	CaptureLogs           *bool `json:"capture_logs,omitempty"`
	CaptureLogsCamel      *bool `json:"captureLogs,omitempty"`
	CaptureSnapshots      *bool `json:"capture_snapshots,omitempty"`
	CaptureSnapshotsCamel *bool `json:"captureSnapshots,omitempty"`
}

func (r runNowRequest) captureHAR() (bool, bool) {
	if r.CaptureHAR != nil {
		return *r.CaptureHAR, true
	}
	if r.CaptureHARCamel != nil {
		return *r.CaptureHARCamel, true
	}
	return false, false
}

func (r runNowRequest) captureLogs() (bool, bool) {
	if r.CaptureLogs != nil {
		return *r.CaptureLogs, true
	}
	if r.CaptureLogsCamel != nil {
		return *r.CaptureLogsCamel, true
	}
	return false, false
}

func (r runNowRequest) captureSnapshots() (bool, bool) {
	if r.CaptureSnapshots != nil {
		return *r.CaptureSnapshots, true
	}
	if r.CaptureSnapshotsCamel != nil {
		return *r.CaptureSnapshotsCamel, true
	}
	return false, false
}

func RunNowHandler(c echo.Context) error {
	id := c.Param("id")
	baseCtx := c.Request().Context().(context.Context)

	scraper, err := db.FindScraper(baseCtx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error()) // could mean server errors as well, but there's no trivial way to find out...
	} else if scraper == nil {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("scraper with id=%s was not found", id))
	}

	configScraper, err := v1.ScrapeConfigFromModel(*scraper)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to transform config scraper model", err)
	}

	req, err := parseRunNowRequest(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	runOpts := runScraperOptsFromRunNowRequest(req)
	isAsync := req.Async != nil && *req.Async

	if isAsync {
		return runNowAsync(c, baseCtx, *scraper, configScraper, runOpts...)
	}
	return runNowSync(c, baseCtx, configScraper, runOpts...)
}

func runNowSync(c echo.Context, baseCtx context.Context, configScraper v1.ScrapeConfig, runOpts ...RunScraperOption) error {
	resultCh := make(chan struct {
		resp runNowResponse
		err  error
	}, 1)
	go func() {
		ctx, cancel := context.New().
			WithDB(baseCtx.DB(), baseCtx.Pool()).
			WithSubject(baseCtx.Subject()).
			WithTimeout(runNowTimeout)
		defer cancel()

		scrapeCtx := api.NewScrapeContext(ctx).WithScrapeConfig(&configScraper)
		j := newScraperJob(scrapeCtx, runOpts...)
		j.JitterDisable = true
		j.Run()

		if j.LastJob == nil {
			resultCh <- struct {
				resp runNowResponse
				err  error
			}{err: fmt.Errorf("scraper run completed without job history")}
			return
		}

		resultCh <- struct {
			resp runNowResponse
			err  error
		}{
			resp: responseFromHistory(j.LastJob),
			err:  j.LastJob.AsError(),
		}
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			return dutyAPI.WriteError(c, result.err)
		}
		return dutyAPI.WriteSuccess(c, result.resp)
	case <-c.Request().Context().Done():
		return c.Request().Context().Err()
	}
}

func runNowAsync(c echo.Context, baseCtx context.Context, scraper models.ConfigScraper, configScraper v1.ScrapeConfig, runOpts ...RunScraperOption) error {
	startedAt := time.Now().UTC()
	go func() {
		ctx, cancel := context.New().
			WithDB(baseCtx.DB(), baseCtx.Pool()).
			WithSubject(baseCtx.Subject()).
			WithTimeout(runNowTimeout)
		defer cancel()

		scrapeCtx := api.NewScrapeContext(ctx).WithScrapeConfig(&configScraper)
		j := newScraperJob(scrapeCtx, runOpts...)
		j.JitterDisable = true
		j.Run()
	}()

	history, err := waitForStartedJobHistory(c.Request().Context(), baseCtx, scraper.ID.String(), startedAt, 10*time.Second)
	if err != nil {
		return dutyAPI.WriteError(c, err)
	}

	resp := responseFromHistory(history)
	if resp.Status == "" {
		resp.Status = models.StatusRunning
	}
	return dutyAPI.WriteSuccess(c, resp)
}

func waitForStartedJobHistory(reqCtx gocontext.Context, baseCtx context.Context, scraperID string, startedAt time.Time, timeout time.Duration) (*models.JobHistory, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		var history models.JobHistory
		err := baseCtx.DB().
			Where("resource_id = ? AND name = ? AND time_start >= ?", scraperID, scrapeJobName, startedAt).
			Order("time_start ASC, id ASC").
			First(&history).Error

		if err == nil {
			return &history, nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		select {
		case <-reqCtx.Done():
			return nil, reqCtx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("timed out waiting for job history")
		case <-ticker.C:
		}
	}
}

func responseFromHistory(history *models.JobHistory) runNowResponse {
	resp := runNowResponse{
		JobHistoryID: history.ID.String(),
		Status:       history.Status,
	}
	if raw, ok := history.Details["run_artifact_id"].(string); ok {
		resp.RunArtifactID = raw
	}
	return resp
}

func parseRunNowRequest(c echo.Context) (runNowRequest, error) {
	var req runNowRequest

	if c.Request().Body == nil {
		return req, nil
	}

	limitedBody := http.MaxBytesReader(c.Response(), c.Request().Body, runNowRequestMaxBytes)
	c.Request().Body = limitedBody

	decoder := json.NewDecoder(limitedBody)
	if err := decoder.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return req, nil
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return req, fmt.Errorf("request body too large (max %d bytes)", runNowRequestMaxBytes)
		}
		return req, fmt.Errorf("invalid request body: %w", err)
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return req, nil
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return req, fmt.Errorf("request body too large (max %d bytes)", runNowRequestMaxBytes)
		}
		return req, fmt.Errorf("invalid request body: %w", err)
	}

	return req, fmt.Errorf("invalid request body: multiple JSON values are not allowed")
}

func runScraperOptsFromRunNowRequest(req runNowRequest) []RunScraperOption {
	opts := make([]RunScraperOption, 0, 3)

	if enabled, ok := req.captureHAR(); ok {
		opts = append(opts, WithCaptureHAR(enabled))
	}
	if enabled, ok := req.captureLogs(); ok {
		opts = append(opts, WithCaptureLogs(enabled))
	}
	if enabled, ok := req.captureSnapshots(); ok {
		opts = append(opts, WithCaptureSnapshots(enabled))
	}

	return opts
}
