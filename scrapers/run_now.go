package scrapers

import (
	"fmt"
	"net/http"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/labstack/echo/v4"
)

func RunNowHandler(c echo.Context) error {
	id := c.Param("id")

	scraper, err := db.FindScraper(api.DefaultContext, id)
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

	ctx := api.DefaultContext.WithScrapeConfig(&configScraper)
	j := newScraperJob(ctx)
	j.Run()
	res := v1.RunNowResponse{
		Total:   j.LastJob.SuccessCount + j.LastJob.ErrorCount,
		Failed:  j.LastJob.ErrorCount,
		Success: j.LastJob.SuccessCount,
		Errors:  j.LastJob.Errors,
	}

	return c.JSON(http.StatusOK, res)
}
