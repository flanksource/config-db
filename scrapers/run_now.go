package scrapers

import (
	"fmt"
	"net/http"
	"time"

	dutyAPI "github.com/flanksource/duty/api"
	"github.com/flanksource/duty/context"
	"github.com/labstack/echo/v4"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/pkg/api"
)

const runNowTimeout = 30 * time.Minute

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

	resultCh := make(chan error, 1)
	go func() {
		defer close(resultCh)

		ctx, cancel := context.New().
			WithDB(baseCtx.DB(), baseCtx.Pool()).
			WithSubject(baseCtx.Subject()).
			WithTimeout(runNowTimeout)
		defer cancel()

		scrapeCtx := api.NewScrapeContext(ctx).WithScrapeConfig(&configScraper)
		j := newScraperJob(scrapeCtx)
		j.JitterDisable = true
		j.Run()

		var runErr error
		if j.LastJob == nil {
			runErr = fmt.Errorf("scraper run completed without job history")
		} else {
			runErr = j.LastJob.AsError()
		}
		resultCh <- runErr
	}()

	select {
	case err := <-resultCh:
		if err != nil {
			return dutyAPI.WriteError(c, err)
		}
		return dutyAPI.WriteSuccess(c, nil)

	case <-c.Request().Context().Done():
		return c.Request().Context().Err()
	}
}
