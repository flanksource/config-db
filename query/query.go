package query

import (
	"net/http"

	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db"
	"github.com/labstack/echo/v4"
)

// Handler ...
func Handler(c echo.Context) error {
	request := v1.QueryRequest{
		Query: c.QueryParam("query"),
	}

	resp, err := db.QueryConfigItems(request)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSONPretty(http.StatusOK, resp, "  ")

}
