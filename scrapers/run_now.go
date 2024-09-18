package scrapers

import (
	"fmt"
	"net/http"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty/context"
	"github.com/labstack/echo/v4"
)

func RunNowHandler(c echo.Context) error {
	id := c.Param("id")

	baseCtx := c.Request().Context()
	ctx := baseCtx.(context.Context)

	scraper, err := db.FindScraper(ctx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error()) // could mean server errors as well, but there's no trivial way to find out...
	}

	if scraper == nil {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("scraper with id=%s was not found", id))
	}

	configScraper, err := v1.ScrapeConfigFromModel(*scraper)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to transform config scraper model", err)
	}

	scrapeCtx := api.NewScrapeContext(ctx).WithScrapeConfig(&configScraper)
	j := newScraperJob(scrapeCtx)
	j.Run()

	return c.JSON(http.StatusOK, j.LastJob.Details)
}
