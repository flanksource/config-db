package query

import (
	"net/http"

	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db"
	"github.com/labstack/echo/v4"
)

func Handler(c echo.Context) error {
	request := v1.QueryRequest{
		Query: c.QueryParam("query"),
	}

	if resp, err := db.QueryConfigItems(request); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	} else {
		return c.JSONPretty(http.StatusOK, resp, "  ")
	}
}
