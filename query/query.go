package query

import (
	"net/http"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/labstack/echo/v4"
)

// Handler ...
func Handler(c echo.Context) error {
	request := v1.QueryRequest{
		Query: c.QueryParam("query"),
	}

	resp, err := db.QueryConfigItems(api.DefaultContext, request)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSONPretty(http.StatusOK, resp, "  ")

}
