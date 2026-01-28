package sql

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/samber/lo"
	"github.com/xo/dburl"

	//drivers
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
)

type SqlScraper struct {
}

func (s SqlScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.SQL) > 0
}

func (s SqlScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, _config := range ctx.ScrapeConfig().Spec.SQL {
		var (
			config     = _config
			err        error
			connection = config.Connection.GetModel()
		)

		if strings.HasPrefix(config.Connection.Connection, "connection://") {
			connection, err = ctx.DutyContext().HydrateConnectionByURL(config.Connection.Connection)
			if err != nil {
				results.Errorf(err, "failed to hydrate connection name %s", config.Connection.Connection)
				continue
			}
			if connection == nil {
				results.Errorf(fmt.Errorf("connection not found"), "failed to find connection name %s", config.Connection.Connection)
				continue
			}
		} else {
			connection, err = ctx.HydrateConnection(connection)
			if err != nil {
				results.Errorf(err, "failed to hydrate connection for %s", config.Connection)
				continue
			}
		}

		db, err := dburl.Open(connection.URL)
		if err != nil {
			results.Errorf(err, "failed to open connection to %s", config.GetEndpoint())
			continue
		}
		defer db.Close() // nolint:errcheck

		if ctx.IsDebug() {
			ctx.Logger.Infof(clicky.CodeBlock("sql", lo.Ellipsis(config.Query, 500)).ANSI())
		}

		rows, err := QuerySQL(db, config.Query)
		if err != nil {
			results.Errorf(err, "failed to query %s", config.GetEndpoint())
			continue
		}

		if ctx.IsTrace() {
			d, _ := clicky.Format(rows)
			ctx.Logger.Infof("SQL response: %d rows\n%s", rows.Count, d)
		}

		for _, _row := range rows.Rows {
			var row = _row
			var item interface{}
			item = row
			if len(rows.Columns) == 1 {
				// if there is only a single column, return the value of that column
				item = row[rows.Columns[0]]
			}
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Config:      item,
				// Do not omit nil in SQL Scraper to retain structure
				OmitNilFields: lo.ToPtr(false),
			})
		}

	}

	return results
}

type SQLDetails struct {
	Columns []string
	Rows    []map[string]any `json:"rows,omitempty"`
	Count   int              `json:"count,omitempty"`
}

// Connects to a db using the specified `driver` and `connectionstring`
// Performs the test query given in `query`.
// Gives the single row test query result as result.
func QuerySQL(db *sql.DB, query string) (*SQLDetails, error) {
	rows, err := db.Query(query)
	result := SQLDetails{}
	if err != nil || rows.Err() != nil {
		return nil, fmt.Errorf("failed to query db: %s", err.Error())
	}
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %s", err.Error())
	}
	result.Columns = columns
	for rows.Next() {
		var rowValues = make([]interface{}, len(columns))
		for i := range rowValues {
			var s sql.NullString
			rowValues[i] = &s
		}
		if err := rows.Scan(rowValues...); err != nil {
			return nil, fmt.Errorf("error scanning rows: %w", err)
		}
		var row = make(map[string]interface{})
		for i, val := range rowValues {
			v := *val.(*sql.NullString)
			if v.Valid {
				row[columns[i]] = v.String
			} else {
				row[columns[i]] = nil
			}
		}
		result.Rows = append(result.Rows, row)
	}
	result.Count = len(result.Rows)
	return &result, nil
}
