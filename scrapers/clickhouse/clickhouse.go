package clickhouse

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
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
		db, err := sql.Open("clickhouse", clickhouseURL)
		if err != nil {
			results.Errorf(err, "failed to open clickhouse connection")
			continue
		}
		if config.AWSS3 != nil {
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(0)
		}

		if err := db.Ping(); err != nil {
			results.Errorf(err, "failed to ping clickhouse")
			_ = db.Close()
			continue
		}

		var qr *cdbsql.SQLDetails
		if config.AWSS3 != nil {
			qr, err = queryAWSS3TemporaryTable(ctx, config, db)
		} else {
			if config.AzureBlobStorage != nil {
				if err := createNamedCollectionForStorage(ctx, config, db); err != nil {
					results.Errorf(err, "failed to create named collection for storage")
					_ = db.Close()
					continue
				}
			}
			qr, err = cdbsql.QuerySQL(db, config.Query)
		}
		if closeErr := db.Close(); closeErr != nil {
			results.Errorf(closeErr, "failed to close clickhouse connection")
		}
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

var clickhouseIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (nc NamedCollection) ToCommands() ([]string, error) {
	if err := validateClickhouseIdentifier(nc.Name); err != nil {
		return nil, err
	}

	dropCmd := fmt.Sprintf("DROP NAMED COLLECTION IF EXISTS %s;", nc.Name)
	keys := make([]string, 0, len(nc.Values))
	for k := range nc.Values {
		if err := validateClickhouseIdentifier(k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		vals = append(vals, fmt.Sprintf("%s=%s", k, clickhouseString(nc.Values[k])))
	}
	createCmd := fmt.Sprintf(`CREATE NAMED COLLECTION %s AS %s;`, nc.Name, strings.Join(vals, ","))
	return []string{dropCmd, createCmd}, nil
}

func (nc NamedCollection) Upsert(ctx api.ScrapeContext, conn *sql.DB) error {
	commands, err := nc.ToCommands()
	if err != nil {
		return err
	}
	for _, cmd := range commands {
		if _, err := conn.ExecContext(ctx, cmd); err != nil {
			return err
		}
	}
	return nil
}

func (nc NamedCollection) Drop(ctx api.ScrapeContext, conn *sql.DB) error {
	if err := validateClickhouseIdentifier(nc.Name); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, fmt.Sprintf("DROP NAMED COLLECTION IF EXISTS %s;", nc.Name))
	return err
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

	default:
		return fmt.Errorf("no valid endpoint")
	}

	return nc.Upsert(ctx, conn)
}

// queryAWSS3TemporaryTable pins one ClickHouse session so the S3 temp table and
// its credentials are visible only to the user query running in that session.
func queryAWSS3TemporaryTable(ctx api.ScrapeContext, config v1.Clickhouse, db *sql.DB) (*cdbsql.SQLDetails, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck

	if err := createAWSS3TemporaryTable(ctx, conn, config.AWSS3); err != nil {
		return nil, err
	}
	return cdbsql.QuerySQLContext(ctx, conn, config.Query)
}

// createAWSS3TemporaryTable resolves whatever AWS auth the scraper has into
// SigV4 credentials and installs them in a connection-scoped S3 table.
func createAWSS3TemporaryTable(ctx api.ScrapeContext, conn *sql.Conn, s3Config *v1.AWSS3) error {
	awsConnection := s3Config.AWSConnection
	if awsConnection == nil {
		awsConnection = &v1.AWSConnection{}
	}

	region := firstNonEmpty(firstRegion(awsConnection.Regions), os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
	awsConn := awsConnection.ToDutyAWSConnection(region)
	if err := awsConn.Populate(ctx); err != nil {
		return err
	}

	session, err := awsConn.Client(ctx.Context)
	if err != nil {
		return fmt.Errorf("failed to create AWS session: %w", err)
	}
	creds, err := session.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve AWS credentials: %w", err)
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return fmt.Errorf("AWS credentials resolved without access key or secret key")
	}

	cmd, err := awss3TemporaryTableSQL(s3Config, creds, region)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, cmd)
	return err
}

func awss3TemporaryTableSQL(s3Config *v1.AWSS3, creds awssdk.Credentials, region string) (string, error) {
	table := lo.CoalesceOrEmpty(s3Config.Table, "cloudtrail")
	if err := validateClickhouseIdentifier(table); err != nil {
		return "", err
	}

	s3URL := awsS3URL(s3Config, region)
	if s3URL == "" {
		return "", fmt.Errorf("awsS3.bucket is required")
	}

	structure := lo.CoalesceOrEmpty(s3Config.Structure, "json String")
	format := lo.CoalesceOrEmpty(s3Config.Format, "JSONAsString")
	args := []string{clickhouseString(s3URL), clickhouseString(creds.AccessKeyID), clickhouseString(creds.SecretAccessKey)}
	if creds.SessionToken != "" {
		args = append(args, clickhouseString(creds.SessionToken))
	}
	args = append(args, clickhouseString(format), clickhouseString(structure))
	if s3Config.Compression != "" {
		args = append(args, clickhouseString(s3Config.Compression))
	}

	return fmt.Sprintf("CREATE TEMPORARY TABLE %s AS s3(%s);", table, strings.Join(args, ",")), nil
}

func awsS3URL(s3Config *v1.AWSS3, region string) string {
	if s3Config.Bucket == "" {
		return ""
	}

	path := strings.Trim(s3Config.Path, "/")
	endpoint := strings.TrimRight(s3Config.Endpoint, "/")
	if endpoint == "" {
		if region != "" {
			return joinURLParts(fmt.Sprintf("https://%s.s3.%s.amazonaws.com", s3Config.Bucket, region), path)
		}
		return joinURLParts(fmt.Sprintf("https://%s.s3.amazonaws.com", s3Config.Bucket), path)
	}

	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}
	if strings.Contains(endpoint, "{bucket}") {
		return joinURLParts(strings.ReplaceAll(endpoint, "{bucket}", s3Config.Bucket), path)
	}
	return joinURLParts(endpoint, s3Config.Bucket, path)
}

func firstRegion(regions []string) string {
	if len(regions) == 0 {
		return ""
	}
	return regions[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinURLParts(base string, parts ...string) string {
	out := strings.TrimRight(base, "/")
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		out += "/" + part
	}
	return out
}

func validateClickhouseIdentifier(name string) error {
	if !clickhouseIdentifier.MatchString(name) {
		return fmt.Errorf("invalid clickhouse identifier %q", name)
	}
	return nil
}

func clickhouseString(value string) string {
	value = strings.ReplaceAll(value, `'`, `''`)
	return "'" + value + "'"
}
