package query

import (
	"errors"
	"net/http"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func RunNowHandler(c echo.Context) error {
	var req v1.RunNowRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	scraper, err := db.GetScraper(req.ScraperID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "scraper with that id was not found.")
		}

		return echo.NewHTTPError(http.StatusBadRequest, err.Error()) // could mean server errors as well, but there's no trivial way to find out...
	}

	configScraper, err := scraper.V1ConfigScraper()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to transform config scraper model.", err)
	}

	if err := scrapers.RunScraper(configScraper); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to run scraper.", err)
	}

	return nil
}
