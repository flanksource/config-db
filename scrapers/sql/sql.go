package sql

import (
	"database/sql"
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/xo/dburl"

	//drivers
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
)

type SqlScraper struct {
}

func (s SqlScraper) CanScrape(configs v1.ConfigScraper) bool {
	return len(configs.SQL) > 0
}

func (s SqlScraper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, _config := range configs.SQL {
		var (
			config     = _config
			connection = config.Connection.GetModel()
		)

		if _connection, err := ctx.HydrateConnectionByURL(config.Connection.Connection); err != nil {
			results.Errorf(err, "failed to find connection")
			continue
		} else if _connection != nil {
			connection = _connection
		}

		db, err := dburl.Open(connection.URL)
		if err != nil {
			results.Errorf(err, "failed to open connection to %s", config.GetEndpoint())
			continue
		}
		defer db.Close()

		rows, err := querySQL(db, config.Query)
		if err != nil {
			results.Errorf(err, "failed to query %s", config.GetEndpoint())
			continue
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
			})
		}

	}

	return results
}

type SQLDetails struct {
	Columns []string
	Rows    []map[string]interface{} `json:"rows,omitempty"`
	Count   int                      `json:"count,omitempty"`
}

// Connects to a db using the specified `driver` and `connectionstring`
// Performs the test query given in `query`.
// Gives the single row test query result as result.
func querySQL(db *sql.DB, query string) (*SQLDetails, error) {
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
			return nil, err
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
