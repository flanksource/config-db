package aws

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/utils/scraper"
	"github.com/flanksource/duty"
	"github.com/henvic/httpretty"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// NewSession ...
func NewSession(ctx *v1.ScrapeContext, conn v1.AWSConnection, region string) (*aws.Config, error) {
	cfg, err := loadConfig(ctx, conn, region)
	if err != nil {
		return nil, err
	}
	if conn.AssumeRole != "" {
		cfg.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(sts.NewFromConfig(*cfg), conn.AssumeRole))
	}
	return cfg, nil
}

// EndpointResolver ...
type EndpointResolver struct {
	Endpoint string
}

// ResolveEndpoint ...
func (e EndpointResolver) ResolveEndpoint(service, region string, options ...interface{}) (aws.Endpoint, error) {
	return aws.Endpoint{
		URL:               e.Endpoint,
		HostnameImmutable: true,
		Source:            aws.EndpointSourceCustom,
	}, nil
}

func loadConfig(ctx *v1.ScrapeContext, conn v1.AWSConnection, region string) (*aws.Config, error) {
	var tr http.RoundTripper
	tr = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: conn.SkipTLSVerify},
	}

	if ctx.IsTrace() {
		httplogger := &httpretty.Logger{
			Time:           true,
			TLS:            true,
			RequestHeader:  true,
			RequestBody:    true,
			ResponseHeader: true,
			ResponseBody:   true,
			Colors:         true, // erase line if you don't like colors
			Formatters:     []httpretty.Formatter{&httpretty.JSONFormatter{}},
		}
		tr = httplogger.RoundTripper(tr)
	}

	options := []func(*config.LoadOptions) error{
		config.WithRegion(region),
		config.WithHTTPClient(&http.Client{Transport: tr}),
	}

	if conn.Endpoint != "" {
		options = append(options, config.WithEndpointResolverWithOptions(EndpointResolver{Endpoint: conn.Endpoint}))
	}

	if !conn.AccessKey.IsEmpty() {
		accessKey, secretKey, err := getAccessAndSecretKey(ctx, conn)
		if err != nil {
			return nil, err
		}
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), options...)
	return &cfg, err
}

func getAccessAndSecretKey(ctx *v1.ScrapeContext, conn v1.AWSConnection) (string, string, error) {
	connection := conn.GetModel()

	if _connection, err := scraper.FindConnectionFromConnectionString(ctx, db.DefaultDB(), connection.Username); err != nil {
		return "", "", fmt.Errorf("failed to find connection from (username=%s): %w", connection.Username, err)
	} else if _connection != nil {
		connection = _connection
	}

	connection, err := duty.HydrateConnection(ctx, ctx.Kubernetes, db.DefaultDB(), connection, ctx.GetNamespace())
	if err != nil {
		return "", "", fmt.Errorf("failed to hydrate connection: %w", err)
	}

	return connection.Username, connection.Password, nil
}
