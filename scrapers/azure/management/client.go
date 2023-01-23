package management

import (
	"context"
	"fmt"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/confidential"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"
	"net/http"
	"os"
)

// AzureManagementClient is the entrypoint into the azure management functionality. It configures the context.
type AzureManagementClient struct {
	Organisation string
	Log          *zap.SugaredLogger
	*resty.Client
	*v1.ScrapeContext
}

// NewAzureManagementClient creates a new AzureManagement Client.
func NewAzureManagementClient(ctx *v1.ScrapeContext, log *zap.SugaredLogger, subscriptionId string, baseUrl string) (*AzureManagementClient, error) {
	client := resty.New().
		SetBaseURL(baseUrl).
		SetPathParam("subscriptionId", subscriptionId)
	return &AzureManagementClient{
		Log:           log,
		ScrapeContext: ctx,
		Client:        client,
	}, nil
}

// GetToken gives us a token for accessing the azure management API.
func (azure *AzureManagementClient) GetToken() string {
	clientID := os.Getenv("AZURE_CLIENT_ID")
	secret := os.Getenv("AZURE_CLIENT_SECRET")
	tenantId := os.Getenv("AZURE_TENANT_ID")

	cred, err := confidential.NewCredFromSecret(secret)
	if err != nil {
		azure.Log.Fatal(err)
	}

	app, err := confidential.New(clientID, cred, confidential.WithAuthority(MicrosoftAuthorityHost+tenantId))
	if err != nil {
		azure.Log.Fatal(errors.Verbose(err))
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
			azure.Log.Fatal(errors.Verbose(er))
		}
		accessToken = result.AccessToken
	}
	result, _ = app.AcquireTokenSilent(context.Background(), scopes)
	if result.AccessToken == "" {
		azure.Log.Fatal(errors.Verbose(err))
	}
	return accessToken
}

// ListResourceGroups returns a list of resource groups.
func (azure *AzureManagementClient) ListResourceGroups(token string) ([]*v1.ResourceGroup, error) {
	var response v1.ResourceGroupListResult

	res, err := azure.R().
		SetAuthToken(token).
		SetQueryString("api-version=2020-09-01").
		SetResult(&response).
		Get("/resourcegroups")
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListKubernetesClusters returns a list of kubernetes clusters.
func (azure *AzureManagementClient) ListKubernetesClusters(token string) ([]*v1.KubernetesCluster, error) {
	var response v1.KubernetesClusterListResult

	res, err := azure.R().
		SetAuthToken(token).
		SetQueryString("api-version=2022-11-01").
		SetResult(&response).
		Get("/providers/Microsoft.ContainerService/managedClusters")
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListContainerRegistries returns a list of container registries.
func (azure *AzureManagementClient) ListContainerRegistries(token string) ([]*v1.ContainerRegistry, error) {
	var response v1.ContainerRegistryListResult

	res, err := azure.R().
		SetAuthToken(token).
		SetQueryString("api-version=2019-05-01").
		SetResult(&response).
		Get("/providers/Microsoft.ContainerRegistry/registries")
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListVirtualMachines returns a list of virtual machines in a resource group.
func (azure *AzureManagementClient) ListVirtualMachines(token string, resourceGroup string) ([]*v1.VirtualMachine, error) {
	var response v1.VirtualMachineListResult

	res, err := azure.R().
		SetAuthToken(token).
		SetQueryString("api-version=2022-11-01").
		SetResult(&response).
		Get(fmt.Sprintf("/resourcegroups/%s/providers/Microsoft.Compute/virtualMachines", resourceGroup))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListLoadBalancers returns a list of load balancers in a resource group.
func (azure *AzureManagementClient) ListLoadBalancers(token string, resourceGroup string) ([]*v1.LoadBalancer, error) {
	var response v1.LoadBalancerListResult

	res, err := azure.R().
		SetAuthToken(token).
		SetQueryString("api-version=2022-07-01").
		SetResult(&response).
		Get(fmt.Sprintf("/resourceGroups/%s/providers/Microsoft.Network/loadBalancers", resourceGroup))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListVirtualNetworks returns a list of virtual networks.
func (azure *AzureManagementClient) ListVirtualNetworks(token string) ([]*v1.VirtualNetwork, error) {
	var response v1.VirtualNetworkListResult

	res, err := azure.R().
		SetAuthToken(token).
		SetQueryString("api-version=2022-07-01").
		SetResult(&response).
		Get("/providers/Microsoft.Network/virtualNetworks")
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListFirewalls returns a list of firewalls.
func (azure *AzureManagementClient) ListFirewalls(token string) ([]*v1.Firewall, error) {
	var response v1.FirewallListResult

	res, err := azure.R().
		SetAuthToken(token).
		SetQueryString("api-version=2022-07-01").
		SetResult(&response).
		Get("/providers/Microsoft.Network/azureFirewalls")
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListDatabases returns a list of databases.
func (azure *AzureManagementClient) ListDatabases(token string) ([]*v1.Database, error) {
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
