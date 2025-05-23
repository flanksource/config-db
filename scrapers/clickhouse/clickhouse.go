package clickhouse

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	cdbsql "github.com/flanksource/config-db/scrapers/sql"
	"github.com/flanksource/duty/shell"
	"github.com/samber/lo"
)

type ClickhouseScraper struct{}

var (
	ClickhouseURL = os.Getenv("CLICKHOUSE_URL")
)

func (ClickhouseScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Clickhouse) > 0
}

func (ch ClickhouseScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, config := range ctx.ScrapeConfig().Spec.Clickhouse {
		clickhouseURL := lo.CoalesceOrEmpty(config.ClickhouseURL, ClickhouseURL)
		conn, err := sql.Open("clickhouse", clickhouseURL)
		if err != nil {
			results.Errorf(err, "failed to open clickhouse connection")
			continue
		}
		defer conn.Close() //nolint:errcheck

		if err := conn.Ping(); err != nil {
			results.Errorf(err, "failed to ping clickhouse")
			continue
		}

		if config.AzureBlobStorage != nil || config.AWSS3 != nil {
			if err := createNamedCollectionForStorage(ctx, config, conn); err != nil {
				results.Errorf(err, "failed to create named collection for storage")
				continue
			}
		}

		qr, err := cdbsql.QuerySQL(conn, config.Query)
		if err != nil {
			results.Errorf(err, "failed to query clickhouse: %s", config.Query)
			continue
		}

		for _, row := range qr.Rows {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Config:      row,
			})
		}
	}
	return results
}

type NamedCollection struct {
	Name   string
	Values map[string]string
}

func (nc NamedCollection) ToCommands() []string {
	dropCmd := fmt.Sprintf("DROP NAMED COLLECTION IF EXISTS %s;", nc.Name)
	var vals []string
	for k, v := range nc.Values {
		vals = append(vals, fmt.Sprintf("%s='%s'", k, v))
	}
	createCmd := fmt.Sprintf(`CREATE NAMED COLLECTION %s AS %s;`, nc.Name, strings.Join(vals, ","))
	return []string{dropCmd, createCmd}
}

func (nc NamedCollection) Upsert(ctx api.ScrapeContext, conn *sql.DB) error {
	for _, cmd := range nc.ToCommands() {
		if _, err := conn.ExecContext(ctx, cmd); err != nil {
			return err
		}
	}
	return nil
}

func createNamedCollectionForStorage(ctx api.ScrapeContext, config v1.Clickhouse, conn *sql.DB) error {
	ex := shell.Exec{}
	var nc NamedCollection
	switch {
	case config.AzureBlobStorage != nil:
		ex.Connections.Azure = config.AzureBlobStorage.AzureConnection
		// TODO: Move AzureBlobStorage struct and its functions to duty
		ex.Script = config.AzureBlobStorage.GetAccountKeyCommand()
		out, err := shell.Run(ctx.DutyContext(), ex)
		if err != nil {
			return fmt.Errorf("error generating azure account key: %w", err)
		}
		accountKey := out.Stdout

		nc.Name = lo.CoalesceOrEmpty(config.AzureBlobStorage.CollectionName, "azure_blob_storage")
		nc.Values = map[string]string{
			"container":         config.AzureBlobStorage.Container,
			"blob_path":         config.AzureBlobStorage.Path,
			"connection_string": config.AzureBlobStorage.GetConnectionString(accountKey),
		}

	case config.AWSS3 != nil:
		return fmt.Errorf("not implemented")
	default:
		return fmt.Errorf("no valid endpoint")
	}

	return nc.Upsert(ctx, conn)
}
