package aws

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/kommons"
	"github.com/henvic/httpretty"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

func isEmpty(val kommons.EnvVar) bool {
	return val.Value == "" && val.ValueFrom == nil
}

// NewSession ...
func NewSession(ctx *v1.ScrapeContext, conn v1.AWSConnection) (*aws.Config, error) {
	cfg, err := loadConfig(ctx, conn)
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

func loadConfig(ctx *v1.ScrapeContext, conn v1.AWSConnection) (*aws.Config, error) {
	namespace := ctx.GetNamespace()
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
		config.WithRegion(conn.Region),
		config.WithHTTPClient(&http.Client{Transport: tr}),
	}

	if conn.Endpoint != "" {
		options = append(options, config.WithEndpointResolverWithOptions(EndpointResolver{Endpoint: conn.Endpoint}))
	}

	if !isEmpty(conn.AccessKey) {
		_, accessKey, err := ctx.Kommons.GetEnvValue(conn.AccessKey, namespace)
		if err != nil {
			return nil, fmt.Errorf("could not parse EC2 access key: %v", err)
		}
		_, secretKey, err := ctx.Kommons.GetEnvValue(conn.SecretKey, namespace)
		if err != nil {
			return nil, fmt.Errorf(fmt.Sprintf("Could not parse EC2 secret key: %v", err))
		}
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), options...)
	return &cfg, err
}
