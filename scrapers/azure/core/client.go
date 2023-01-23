package core

import (
	"context"
	"fmt"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/confidential"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/go-resty/resty/v2"
	"net/http"
	"os"
)

// Azure is the entrypoint into the azure core functionality. It configures the context.
type Azure struct {
	Organisation string
	*resty.Client
	*v1.ScrapeContext
}

// NewAzureClient creates a new AzureManagement Client.
func NewAzureClient(ctx *v1.ScrapeContext, subscriptionId string, baseUrl string) (*Azure, error) {
	client := resty.New().
		SetBaseURL(baseUrl).
		SetPathParam("subscriptionId", subscriptionId)
	return &Azure{
		ScrapeContext: ctx,
		Client:        client,
	}, nil
}

// GetToken gives us a token for accessing the azure core API.
func (azure *Azure) GetToken() string {
	clientID := os.Getenv("AZURE_CLIENT_ID")
	secret := os.Getenv("AZURE_CLIENT_SECRET")
	tenantId := os.Getenv("AZURE_TENANT_ID")

	cred, err := confidential.NewCredFromSecret(secret)
	if err != nil {
		logger.Fatalf(errors.Verbose(err))
	}

	app, err := confidential.New(clientID, cred, confidential.WithAuthority(MicrosoftAuthorityHost+tenantId))
	if err != nil {
		logger.Fatalf(errors.Verbose(err))
	}
	scopes := []string{"https://management.azure.com//.default"}

	var accessToken string

	// =========================================================================
	// Msal library comes with an in-memory cache to store tokens. We therefore begin by checking if we have any
	// value in the cache or a refresh token. If we fail, then we default to using Azure Active Directory.
	// AcquireTokenSilent acquires a token from either the cache or using a refresh token.

	result, err := app.AcquireTokenSilent(context.Background(), scopes)
	if err != nil {

		// Token not in cache, we proceed to get the token using Azure Active Directory Oath2.

		result, er := app.AcquireTokenByCredential(context.Background(), scopes)
		if er != nil {
			logger.Fatalf(errors.Verbose(err))
		}
		accessToken = result.AccessToken
	}
	result, _ = app.AcquireTokenSilent(context.Background(), scopes)
	if result.AccessToken == "" {
		logger.Fatalf(errors.Verbose(err))
	}
	return accessToken
}

// ListDatabases returns a list of databases.
func (azure *Azure) ListDatabases(token string) ([]*v1.Database, error) {
	//We are only filtering 2 databases here.

	filter := "ResourceType eq 'Microsoft.DBforPostgreSQL/servers' or ResourceType eq 'Microsoft.Sql"
	var response v1.DatabaseListResult
	res, err := azure.R().
		SetAuthToken(token).
		SetQueryString("api-version=2022-11-01-preview").
		SetResult(&response).
		Get(fmt.Sprintf("/resources?$filter=%s/servers/databases", filter))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}
