package v1

import "testing"

func TestAzureBlobStorageGetAccountKeyCommand(t *testing.T) {
	config := AzureBlobStorage{Account: "account'name"}
	want := `az storage account keys list --account-name 'account'"'"'name' --query '[0].value' --output tsv`
	if got := config.GetAccountKeyCommand(); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestAzureBlobStorageGetConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name: "azure cloud default",
			want: "DefaultEndpointsProtocol=https;AccountName=logs;AccountKey=secret;EndpointSuffix=core.windows.net",
		},
		{
			name:     "custom cloud suffix",
			endpoint: "core.usgovcloudapi.net",
			want:     "DefaultEndpointsProtocol=https;AccountName=logs;AccountKey=secret;EndpointSuffix=core.usgovcloudapi.net",
		},
		{
			name:     "emulator blob endpoint",
			endpoint: "http://azurite:10000/logs",
			want:     "DefaultEndpointsProtocol=http;AccountName=logs;AccountKey=secret;BlobEndpoint=http://azurite:10000/logs",
		},
		{
			name:     "explicit blob endpoint path is preserved",
			endpoint: "https://storage.example.test/custom/account/",
			want:     "DefaultEndpointsProtocol=https;AccountName=logs;AccountKey=secret;BlobEndpoint=https://storage.example.test/custom/account",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := AzureBlobStorage{Account: "logs", EndpointSuffix: tt.endpoint}
			if got := config.GetConnectionString("secret"); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
