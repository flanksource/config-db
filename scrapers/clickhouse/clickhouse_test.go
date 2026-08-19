package clickhouse

import (
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	v1 "github.com/flanksource/config-db/api/v1"
)

func TestNamedCollectionCommands(t *testing.T) {
	commands, err := (NamedCollection{
		Name: "aws_s3",
		Values: map[string]string{
			"secret_access_key": "sec'ret",
			"access_key_id":     "AKIA",
		},
	}).ToCommands()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"DROP NAMED COLLECTION IF EXISTS aws_s3;",
		"CREATE NAMED COLLECTION aws_s3 AS access_key_id='AKIA',secret_access_key='sec''ret';",
	}
	if len(commands) != len(want) {
		t.Fatalf("expected %d commands, got %d", len(want), len(commands))
	}
	for i := range want {
		if commands[i] != want[i] {
			t.Fatalf("command %d: expected %q, got %q", i, want[i], commands[i])
		}
	}
}

func TestNamedCollectionRejectsInvalidIdentifier(t *testing.T) {
	_, err := (NamedCollection{Name: "aws_s3; DROP TABLE system.one", Values: map[string]string{"url": "s3://bucket"}}).ToCommands()
	if err == nil {
		t.Fatal("expected invalid identifier error")
	}
}

func TestAWSS3TemporaryTableSQL(t *testing.T) {
	cmd, err := awss3TemporaryTableSQL(&v1.AWSS3{
		Bucket:    "cloudtrail",
		Path:      "AWSLogs/123/*.json.gz",
		Format:    "JSONAsString",
		Structure: "json String",
	}, awssdk.Credentials{
		AccessKeyID:     "ASIA",
		SecretAccessKey: `sec'ret\value`,
		SessionToken:    "token",
	}, "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `CREATE TEMPORARY TABLE scrape_table (json String) ENGINE = S3('https://cloudtrail.s3.us-east-1.amazonaws.com/AWSLogs/123/*.json.gz','ASIA','sec''ret\\value','token','JSONAsString');`
	if cmd != want {
		t.Fatalf("expected %q, got %q", want, cmd)
	}
}

func TestAWSS3TemporaryTableSQLDefaults(t *testing.T) {
	cmd, err := awss3TemporaryTableSQL(&v1.AWSS3{Bucket: "cloudtrail"}, awssdk.Credentials{AccessKeyID: "AKIA", SecretAccessKey: "secret"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "CREATE TEMPORARY TABLE scrape_table (json String) ENGINE = S3('https://cloudtrail.s3.amazonaws.com','AKIA','secret','JSONAsString');"
	if cmd != want {
		t.Fatalf("expected %q, got %q", want, cmd)
	}
}

func TestAWSS3TemporaryTableSQLValidation(t *testing.T) {
	tests := []struct {
		name   string
		config *v1.AWSS3
	}{
		{name: "nil config"},
		{name: "missing bucket", config: &v1.AWSS3{}},
		{name: "blank structure", config: &v1.AWSS3{Bucket: "cloudtrail", Structure: "  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := awss3TemporaryTableSQL(tt.config, awssdk.Credentials{}, "us-east-1"); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAWSS3URL(t *testing.T) {
	tests := []struct {
		name   string
		config v1.AWSS3
		region string
		want   string
	}{
		{
			name:   "aws regional virtual host",
			config: v1.AWSS3{Bucket: "cloudtrail", Path: "AWSLogs/123/*"},
			region: "eu-west-1",
			want:   "https://cloudtrail.s3.eu-west-1.amazonaws.com/AWSLogs/123/*",
		},
		{
			name:   "aws global virtual host",
			config: v1.AWSS3{Bucket: "cloudtrail", Path: "/AWSLogs/123/*/"},
			want:   "https://cloudtrail.s3.amazonaws.com/AWSLogs/123/*",
		},
		{
			name:   "dotted aws bucket uses regional path style",
			config: v1.AWSS3{Bucket: "audit.example.com", Path: "AWSLogs/123/*"},
			region: "us-east-1",
			want:   "https://s3.us-east-1.amazonaws.com/audit.example.com/AWSLogs/123/*",
		},
		{
			name:   "dotted aws bucket uses global path style",
			config: v1.AWSS3{Bucket: "audit.example.com", Path: "AWSLogs/123/*"},
			want:   "https://s3.amazonaws.com/audit.example.com/AWSLogs/123/*",
		},
		{
			name:   "path-style endpoint",
			config: v1.AWSS3{Bucket: "cloudtrail", Path: "AWSLogs/123/*", Endpoint: "http://minio:9000"},
			want:   "http://minio:9000/cloudtrail/AWSLogs/123/*",
		},
		{
			name:   "endpoint bucket placeholder",
			config: v1.AWSS3{Bucket: "cloudtrail", Path: "AWSLogs/123/*", Endpoint: "https://{bucket}.s3.local"},
			want:   "https://cloudtrail.s3.local/AWSLogs/123/*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := awsS3URL(&tt.config, tt.region); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestResolveAWSS3Config(t *testing.T) {
	tests := []struct {
		name               string
		config             v1.AWSS3
		connectionEndpoint string
		connectionRegion   string
		clientRegion       string
		wantEndpoint       string
		wantRegion         string
	}{
		{
			name:               "uses hydrated connection values",
			config:             v1.AWSS3{Bucket: "cloudtrail"},
			connectionEndpoint: "http://minio:9000",
			connectionRegion:   "us-east-1",
			wantEndpoint:       "http://minio:9000",
			wantRegion:         "us-east-1",
		},
		{
			name:               "scraper endpoint overrides connection",
			config:             v1.AWSS3{Bucket: "cloudtrail", Endpoint: "http://scraper-minio:9000"},
			connectionEndpoint: "http://connection-minio:9000",
			connectionRegion:   "us-west-2",
			clientRegion:       "eu-west-1",
			wantEndpoint:       "http://scraper-minio:9000",
			wantRegion:         "us-west-2",
		},
		{
			name:         "uses AWS client default region",
			config:       v1.AWSS3{Bucket: "cloudtrail"},
			clientRegion: "ap-southeast-2",
			wantRegion:   "ap-southeast-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalEndpoint := tt.config.Endpoint
			resolved, region := resolveAWSS3Config(&tt.config, tt.connectionEndpoint, tt.connectionRegion, tt.clientRegion)
			if resolved.Endpoint != tt.wantEndpoint {
				t.Fatalf("expected endpoint %q, got %q", tt.wantEndpoint, resolved.Endpoint)
			}
			if region != tt.wantRegion {
				t.Fatalf("expected region %q, got %q", tt.wantRegion, region)
			}
			if tt.config.Endpoint != originalEndpoint {
				t.Fatalf("input config was mutated: expected endpoint %q, got %q", originalEndpoint, tt.config.Endpoint)
			}
		})
	}
}
