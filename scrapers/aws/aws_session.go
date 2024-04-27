package aws

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/henvic/httpretty"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// NewSession ...
func NewSession(ctx api.ScrapeContext, conn v1.AWSConnection, region string) (*aws.Config, error) {
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

func loadConfig(ctx api.ScrapeContext, conn v1.AWSConnection, region string) (*aws.Config, error) {
	if conn.ConnectionName != "" {
		connection, err := ctx.HydrateConnectionByURL(conn.ConnectionName)
		if err != nil {
			return nil, fmt.Errorf("could not hydrate connection: %w", err)
		} else if connection == nil {
			return nil, fmt.Errorf("connection %s not found", conn.ConnectionName)
		}

		conn.AccessKey.ValueStatic = connection.Username
		conn.SecretKey.ValueStatic = connection.Password
		conn.Endpoint = connection.URL
	}

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

// getAccessAndSecretKey retrieves the access and secret keys from the Kubernetes cache.
func getAccessAndSecretKey(ctx api.ScrapeContext, conn v1.AWSConnection) (string, string, error) {
	accessKey, err := ctx.GetEnvValueFromCache(conn.AccessKey, ctx.Namespace())
	if err != nil {
		return "", "", fmt.Errorf("error getting access key: %w", err)
	}

	secretKey, err := ctx.GetEnvValueFromCache(conn.SecretKey, ctx.Namespace())
	if err != nil {
		return "", "", fmt.Errorf("error getting secret key: %w", err)
	}

	return accessKey, secretKey, nil
}
