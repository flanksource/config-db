package clickhouse

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/flanksource/clicky"
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

	configs := ctx.ScrapeConfig().Spec.Clickhouse
	ctx.Logger.Debugf("scraping %d clickhouse config(s)", len(configs))

	for i, config := range configs {
		clickhouseURL := lo.CoalesceOrEmpty(config.ClickhouseURL, ClickhouseURL)
		db, err := sql.Open("clickhouse", clickhouseURL)
		if err != nil {
			results.Errorf(err, "failed to open clickhouse connection")
			continue
		}
		if config.AWSS3 != nil {
			// ClickHouse keeps temporary tables on the connection that created them.
			// Use one connection and close it after the scrape so the AWS credentials do not remain.
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(0)
		}

		if err := db.Ping(); err != nil {
			results.Errorf(err, "failed to ping clickhouse")
			_ = db.Close()
			continue
		}
		ctx.Logger.Tracef("[clickhouse/%d] connection established", i)

		start := time.Now()

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
			ctx.Logger.Debugf("[clickhouse/%d] running query\n%s", i, lazyString(func() string {
				return clicky.CodeBlock("sql", config.Query).ANSI()
			}))
			qr, err = cdbsql.QuerySQL(db, config.Query)
		}
		if closeErr := db.Close(); closeErr != nil {
			results.Errorf(closeErr, "failed to close clickhouse connection")
		}
		if err != nil {
			results.Errorf(err, "failed to query clickhouse: %s", config.Query)
			continue
		}

		ctx.Logger.Debugf("[clickhouse/%d] query returned %d row(s) in %s", i, qr.Count, time.Since(start))
		ctx.Logger.Tracef("[clickhouse/%d] response: columns=%v\n%s", i, qr.Columns, lazyFormat{qr})

		for _, row := range qr.Rows {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Config:      row,
			})
		}
	}
	return results
}

// lazyString defers log argument rendering until the logger emits the line.
type lazyString func() string

func (l lazyString) String() string {
	return l()
}

// lazyFormat renders a whole result set only if the line is actually emitted.
// commons checks the level before it formats its arguments, so String() never
// runs when trace logging is off -- which matters here because the value being
// rendered is every scraped row.
type lazyFormat struct{ value any }

func (l lazyFormat) String() string {
	formatted, err := clicky.Format(l.value)
	if err != nil {
		return fmt.Sprintf("<unformattable: %v>", err)
	}

	return formatted
}

type NamedCollection struct {
	Name   string
	Values map[string]string
}

// awsS3TemporaryTableName is the table available to an S3 scrape query. The scraper
// creates it just before running the query and removes it by closing the connection,
// so users always query this name instead of configuring a table that only lives for one scrape.
const awsS3TemporaryTableName = "scrape_table"

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
	// The commands embed the storage connection string, so only the collection
	// name is logged.
	ctx.Logger.Debugf("upserting named collection %s with keys %v", nc.Name, lo.Keys(nc.Values))
	for _, cmd := range commands {
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

	default:
		return fmt.Errorf("no valid endpoint")
	}

	return nc.Upsert(ctx, conn)
}

// queryAWSS3TemporaryTable pins one ClickHouse session so the S3 temp table and
// its credentials are visible only to the user query running in that session.
func queryAWSS3TemporaryTable(ctx api.ScrapeContext, config v1.Clickhouse, db *sql.DB) (*cdbsql.SQLDetails, error) {
	if config.AWSS3 == nil {
		return nil, fmt.Errorf("awsS3 configuration is required")
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to pin clickhouse connection: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	if err := createAWSS3TemporaryTable(ctx, conn, config.AWSS3); err != nil {
		return nil, err
	}

	ctx.Logger.Debugf("running query against %s\n%s", awsS3TemporaryTableName, lazyString(func() string {
		return clicky.CodeBlock("sql", config.Query).ANSI()
	}))
	return cdbsql.QuerySQLContext(ctx, conn, config.Query)
}

// createAWSS3TemporaryTable resolves whatever AWS auth the scraper has into
// SigV4 credentials and installs them in a connection-scoped S3 table.
func createAWSS3TemporaryTable(ctx api.ScrapeContext, conn *sql.Conn, s3Config *v1.AWSS3) error {
	if s3Config == nil {
		return fmt.Errorf("awsS3 configuration is required")
	}

	awsConnection := s3Config.AWSConnection
	if awsConnection == nil {
		awsConnection = &v1.AWSConnection{}
	}

	// Leave the region empty when the scraper does not specify one so Populate
	// can use connection properties before the AWS SDK applies ambient defaults.
	region := firstRegion(awsConnection.Regions)
	awsConn := awsConnection.ToDutyAWSConnection(region)
	if err := awsConn.Populate(ctx); err != nil {
		return fmt.Errorf("failed to resolve AWS connection: %w", err)
	}

	session, err := awsConn.Client(ctx.Context)
	if err != nil {
		return fmt.Errorf("failed to create AWS session: %w", err)
	}
	if session.Credentials == nil {
		return fmt.Errorf("AWS session has no credential provider")
	}
	creds, err := session.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve AWS credentials: %w", err)
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return fmt.Errorf("AWS credentials resolved without access key or secret key")
	}
	ctx.Logger.Debugf("resolved AWS credentials from %q (session token: %t)",
		lo.CoalesceOrEmpty(creds.Source, "unknown"), creds.SessionToken != "")

	resolvedConfig, resolvedRegion := resolveAWSS3Config(s3Config, awsConn.Endpoint, awsConn.Region, session.Region)
	cmd, err := awss3TemporaryTableSQL(resolvedConfig, creds, resolvedRegion)
	if err != nil {
		return err
	}

	ctx.Logger.Debugf("creating temporary table %s from %s", awsS3TemporaryTableName, awsS3URL(resolvedConfig, resolvedRegion))
	// Re-render the statement with placeholder credentials rather than redacting
	// the real one, so the logged SQL can never leak the keys.
	if masked, maskErr := awss3TemporaryTableSQL(resolvedConfig, maskedCredentials(creds), resolvedRegion); maskErr == nil {
		ctx.Logger.Tracef("%s", lazyString(func() string {
			return clicky.CodeBlock("sql", masked).ANSI()
		}))
	}

	if _, err := conn.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("failed to create clickhouse S3 temporary table %q: %w", awsS3TemporaryTableName, err)
	}
	return nil
}

// maskedCredentials returns credentials whose secrets are replaced by
// placeholders, for rendering a loggable version of an S3 table statement.
func maskedCredentials(creds awssdk.Credentials) awssdk.Credentials {
	masked := awssdk.Credentials{
		AccessKeyID:     "<redacted>",
		SecretAccessKey: "<redacted>",
	}
	if creds.SessionToken != "" {
		masked.SessionToken = "<redacted>"
	}
	return masked
}

func awss3TemporaryTableSQL(s3Config *v1.AWSS3, creds awssdk.Credentials, region string) (string, error) {
	if s3Config == nil {
		return "", fmt.Errorf("awsS3 configuration is required")
	}

	s3URL := awsS3URL(s3Config, region)
	if s3URL == "" {
		return "", fmt.Errorf("awsS3.bucket is required")
	}

	structure := strings.TrimSpace(lo.CoalesceOrEmpty(s3Config.Structure, "json String"))
	if structure == "" {
		return "", fmt.Errorf("awsS3.structure is required")
	}
	format := lo.CoalesceOrEmpty(s3Config.Format, "JSONAsString")
	args := []string{clickhouseString(s3URL), clickhouseString(creds.AccessKeyID), clickhouseString(creds.SecretAccessKey)}
	if creds.SessionToken != "" {
		args = append(args, clickhouseString(creds.SessionToken))
	}
	args = append(args, clickhouseString(format))

	return fmt.Sprintf("CREATE TEMPORARY TABLE %s (%s) ENGINE = S3(%s);", awsS3TemporaryTableName, structure, strings.Join(args, ",")), nil
}

func awsS3URL(s3Config *v1.AWSS3, region string) string {
	if s3Config == nil || s3Config.Bucket == "" {
		return ""
	}

	path := strings.Trim(s3Config.Path, "/")
	endpoint := strings.TrimRight(s3Config.Endpoint, "/")
	if endpoint == "" {
		if strings.Contains(s3Config.Bucket, ".") {
			if region != "" {
				return joinURLParts(fmt.Sprintf("https://s3.%s.amazonaws.com", region), s3Config.Bucket, path)
			}
			return joinURLParts("https://s3.amazonaws.com", s3Config.Bucket, path)
		}
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

// resolveAWSS3Config applies values discovered while hydrating the AWS
// connection without overriding fields set directly on the S3 scraper.
func resolveAWSS3Config(s3Config *v1.AWSS3, connectionEndpoint, connectionRegion, clientRegion string) (*v1.AWSS3, string) {
	resolved := *s3Config
	resolved.Endpoint = firstNonEmpty(s3Config.Endpoint, connectionEndpoint)
	return &resolved, firstNonEmpty(connectionRegion, clientRegion)
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
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `''`)
	return "'" + value + "'"
}
