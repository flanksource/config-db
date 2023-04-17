package scrapers

import (
	"fmt"
	"net/http"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/labstack/echo/v4"
)

func RunNowHandler(c echo.Context) error {
	id := c.Param("id")

	scraper, err := db.FindScraper(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error()) // could mean server errors as well, but there's no trivial way to find out...
	}

	if scraper == nil {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("scraper with id=%s was not found", id))
	}

	configScraper, err := scraper.V1ConfigScraper()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to transform config scraper model", err)
	}

	results, err := RunScraper(configScraper)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to run scraper", err)
	}

	res := v1.RunNowResponse{
		Total:  len(results),
		Errors: results.Errors(),
	}
	res.Failed = len(res.Errors)
	res.Success = res.Total - res.Failed
	return c.JSON(http.StatusOK, res)
}
