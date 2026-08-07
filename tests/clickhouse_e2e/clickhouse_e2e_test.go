//go:build clickhouse_e2e

// Package clickhouse_e2e verifies the ConfigDB binary against real ClickHouse
// object-storage integrations without requiring Kubernetes or a database.
package clickhouse_e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	clickhouseImage   = "clickhouse/clickhouse-server:25.4.13.22"
	minioImage        = "quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z"
	minioAccessKey    = "clickhouse-s3-access-key"
	minioSecretKey    = "clickhouse-s3-secret-key"
	azuriteImage      = "mcr.microsoft.com/azure-storage/azurite:3.35.0"
	azuriteAccount    = "devstoreaccount1"
	azuriteAccountKey = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==" // gitleaks:allow
)

type configRecord struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Type   string         `json:"config_type"`
	Config map[string]any `json:"config"`
}

type runOutput struct {
	Results struct {
		Configs []configRecord `json:"configs"`
	} `json:"results"`
}

func TestClickHouseObjectStorageE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)

	repoRoot := repositoryRoot(t)
	binary := buildConfigDB(t, ctx, repoRoot)
	azPath := installAZShim(t)

	nw, err := network.New(ctx, network.WithDriver("bridge"))
	require.NoError(t, err)
	testcontainers.CleanupNetwork(t, nw)

	minio := startContainer(t, ctx, testcontainers.ContainerRequest{
		Image:        minioImage,
		ExposedPorts: []string{"9000/tcp"},
		Env: map[string]string{
			"MINIO_ROOT_USER":     minioAccessKey,
			"MINIO_ROOT_PASSWORD": minioSecretKey,
		},
		Cmd:            []string{"server", "/data"},
		Networks:       []string{nw.Name},
		NetworkAliases: map[string][]string{nw.Name: {"minio"}},
		WaitingFor: wait.ForHTTP("/minio/health/ready").
			WithPort("9000/tcp").
			WithStartupTimeout(90 * time.Second),
	})

	azurite := startContainer(t, ctx, testcontainers.ContainerRequest{
		Image:          azuriteImage,
		ExposedPorts:   []string{"10000/tcp"},
		Cmd:            []string{"azurite-blob", "--blobHost", "0.0.0.0", "--skipApiVersionCheck"},
		Networks:       []string{nw.Name},
		NetworkAliases: map[string][]string{nw.Name: {"azurite"}},
		WaitingFor: wait.ForLog("Azurite Blob service successfully listens").
			WithStartupTimeout(90 * time.Second),
	})

	clickhouse := startContainer(t, ctx, testcontainers.ContainerRequest{
		Image:        clickhouseImage,
		ExposedPorts: []string{"8123/tcp", "9000/tcp"},
		Env: map[string]string{
			"CLICKHOUSE_SKIP_USER_SETUP": "1",
		},
		Networks:       []string{nw.Name},
		NetworkAliases: map[string][]string{nw.Name: {"clickhouse"}},
		WaitingFor: wait.ForHTTP("/ping").
			WithPort("8123/tcp").
			WithStartupTimeout(2 * time.Minute),
	})

	seedMinIO(t, ctx, minio, filepath.Join(repoRoot, "fixtures", "clickhouse-cloudtrail.json"))
	seedAzurite(t, ctx, azurite, filepath.Join(repoRoot, "fixtures", "clickhouse-azure.jsonl"))
	clickhouseURL := containerClickHouseURL(t, ctx, clickhouse)

	t.Run("S3 CloudTrail", func(t *testing.T) {
		got := runFixture(t, ctx, binary, filepath.Join(repoRoot, "fixtures", "clickhouse-s3.yaml"), clickhouseURL, azPath)
		want := []configRecord{
			{
				ID:   "11111111-1111-4111-8111-111111111111",
				Name: "CreateBucket",
				Type: "AWS::CloudTrail::Event",
				Config: map[string]any{
					"aws_region":   "us-east-1",
					"event_id":     "11111111-1111-4111-8111-111111111111",
					"event_name":   "CreateBucket",
					"event_source": "s3.amazonaws.com",
				},
			},
			{
				ID:   "22222222-2222-4222-8222-222222222222",
				Name: "PutBucketEncryption",
				Type: "AWS::CloudTrail::Event",
				Config: map[string]any{
					"aws_region":   "us-east-1",
					"event_id":     "22222222-2222-4222-8222-222222222222",
					"event_name":   "PutBucketEncryption",
					"event_source": "s3.amazonaws.com",
				},
			},
		}
		require.Equal(t, want, got)
	})

	t.Run("Azure Blob", func(t *testing.T) {
		got := runFixture(t, ctx, binary, filepath.Join(repoRoot, "fixtures", "clickhouse-azure.yaml"), clickhouseURL, azPath)
		want := []configRecord{
			{
				ID:   "storage-001",
				Name: "primary-storage",
				Type: "Azure::Storage::Account",
				Config: map[string]any{
					"resource_id":   "storage-001",
					"resource_name": "primary-storage",
					"sku":           "Standard_LRS",
				},
			},
			{
				ID:   "storage-002",
				Name: "archive-storage",
				Type: "Azure::Storage::Account",
				Config: map[string]any{
					"resource_id":   "storage-002",
					"resource_name": "archive-storage",
					"sku":           "Standard_GRS",
				},
			},
		}
		require.Equal(t, want, got)
	})
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func buildConfigDB(t *testing.T, ctx context.Context, repoRoot string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "config-db")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "building config-db:\n%s", output)
	return binary
}

func installAZShim(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	shim := filepath.Join(dir, "az")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "storage" ] && [ "$2" = "account" ] && [ "$3" = "keys" ] && [ "$4" = "list" ]; then
  printf '%%s\n' '%s'
  exit 0
fi
echo "unexpected az invocation: $*" >&2
exit 1
`, azuriteAccountKey)
	require.NoError(t, os.WriteFile(shim, []byte(script), 0o700))
	return dir
}

func startContainer(t *testing.T, ctx context.Context, request testcontainers.ContainerRequest) testcontainers.Container {
	t.Helper()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: request,
		Started:          true,
	})
	if container != nil {
		testcontainers.CleanupContainer(t, container)
	}
	require.NoError(t, err, "starting %s", request.Image)
	return container
}

func seedMinIO(t *testing.T, ctx context.Context, container testcontainers.Container, fixture string) {
	t.Helper()
	endpoint, err := container.Endpoint(ctx, "http")
	require.NoError(t, err)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(minioAccessKey, minioSecretKey, "")),
	)
	require.NoError(t, err)
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})

	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("cloudtrail")})
	require.NoError(t, err)
	data, err := os.ReadFile(fixture)
	require.NoError(t, err)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err = writer.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("cloudtrail"),
		Key:    aws.String("AWSLogs/123456789012/CloudTrail/us-east-1/2026/07/10/123456789012_CloudTrail_us-east-1_20260710T1200Z_config-db.json.gz"),
		Body:   bytes.NewReader(compressed.Bytes()),
	})
	require.NoError(t, err)
}

func seedAzurite(t *testing.T, ctx context.Context, container testcontainers.Container, fixture string) {
	t.Helper()
	endpoint, err := container.Endpoint(ctx, "http")
	require.NoError(t, err)
	connectionString := fmt.Sprintf(
		"DefaultEndpointsProtocol=http;AccountName=%s;AccountKey=%s;BlobEndpoint=%s/%s",
		azuriteAccount,
		azuriteAccountKey,
		endpoint,
		azuriteAccount,
	)
	client, err := azblob.NewClientFromConnectionString(connectionString, nil)
	require.NoError(t, err)
	_, err = client.CreateContainer(ctx, "config-db", nil)
	require.NoError(t, err)
	data, err := os.ReadFile(fixture)
	require.NoError(t, err)
	_, err = client.UploadBuffer(ctx, "config-db", "resources.jsonl", data, nil)
	require.NoError(t, err)
}

func containerClickHouseURL(t *testing.T, ctx context.Context, container testcontainers.Container) string {
	t.Helper()
	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "9000/tcp")
	require.NoError(t, err)
	return fmt.Sprintf("clickhouse://default@%s/default", net.JoinHostPort(host, port.Port()))
}

func runFixture(t *testing.T, ctx context.Context, binary, fixture, clickhouseURL, azPath string) []configRecord {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary, "run", "--json", fixture)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"CLICKHOUSE_URL="+clickhouseURL,
		"PATH="+azPath+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "config-db stderr:\n%s\nstdout:\n%s", stderr.String(), stdout.String())

	var output runOutput
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &output), "config-db stderr:\n%s\nstdout:\n%s", stderr.String(), stdout.String())
	sort.Slice(output.Results.Configs, func(i, j int) bool {
		return output.Results.Configs[i].ID < output.Results.Configs[j].ID
	})
	return output.Results.Configs
}
