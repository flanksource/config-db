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

const Version = "2020-09-01"

// AzureManagementClient is the entrypoint into the azure management functionality. It configures the context.
type AzureManagementClient struct {
	Organisation string
	Log          *zap.SugaredLogger
	*resty.Client
	*v1.ScrapeContext
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

// NewAzureManagementClient creates a new AzureManagement Client.
func NewAzureManagementClient(ctx *v1.ScrapeContext, log *zap.SugaredLogger, subscriptionId string) (*AzureManagementClient, error) {
	client := resty.New().
		SetBaseURL(fmt.Sprintf("https://management.azure.com/subscriptions/%s", subscriptionId))
	return &AzureManagementClient{
		Log:           log,
		ScrapeContext: ctx,
		Client:        client,
	}, nil
}

// ListResourceGroups returns a list of resource groups.
func (azure *AzureManagementClient) ListResourceGroups() ([]*v1.ResourceGroup, error) {
	var response v1.ResourceGroupListResult

	accessToken := azure.GetToken()
	azure.Client.Header.Add("Authorization", "Bearer "+accessToken)

	res, err := azure.R().SetResult(&response).Get(fmt.Sprintf("/resourcegroups?api-version=%s", Version))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListKubernetesClusters returns a list of kubernetes clusters.
func (azure *AzureManagementClient) ListKubernetesClusters() ([]*v1.KubernetesCluster, error) {
	var response v1.KubernetesClusterListResult

	accessToken := azure.GetToken()
	azure.Client.Header.Add("Authorization", "Bearer "+accessToken)

	res, err := azure.R().SetResult(&response).Get(fmt.Sprintf("/providers/Microsoft.ContainerService/managedClusters?api-version=%s", Version))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListContainerRegistries returns a list of container registries.
func (azure *AzureManagementClient) ListContainerRegistries() ([]*v1.ContainerRegistry, error) {
	var response v1.ContainerRegistryListResult

	accessToken := azure.GetToken()
	azure.Client.Header.Add("Authorization", "Bearer "+accessToken)

	res, err := azure.R().SetResult(&response).Get(fmt.Sprintf("/providers/Microsoft.ContainerRegistry/registries?api-version=%s", Version))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListVirtualMachines returns a list of virtual machines in a resource group.
func (azure *AzureManagementClient) ListVirtualMachines(resourceGroup string) ([]*v1.VirtualMachine, error) {
	var response v1.VirtualMachineListResult

	accessToken := azure.GetToken()
	azure.Client.Header.Add("Authorization", "Bearer "+accessToken)

	res, err := azure.R().SetResult(&response).Get(fmt.Sprintf("/resourcegroups/%s/providers/Microsoft.Compute/virtualMachines?api-version=%s", resourceGroup, Version))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListLoadBalancers returns a list of load balancers in a resource group.
func (azure *AzureManagementClient) ListLoadBalancers(resourceGroup string) ([]*v1.LoadBalancer, error) {
	var response v1.LoadBalancerListResult

	accessToken := azure.GetToken()
	azure.Client.Header.Add("Authorization", "Bearer "+accessToken)

	res, err := azure.R().SetResult(&response).Get(fmt.Sprintf("/resourceGroups/%s/providers/Microsoft.Network/loadBalancers?api-version=%s", resourceGroup, Version))
	r := res.StatusCode()
	fmt.Println(r)
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListVirtualNetworks returns a list of virtual networks.
func (azure *AzureManagementClient) ListVirtualNetworks() ([]*v1.VirtualMachine, error) {
	var response v1.VirtualMachineListResult

	accessToken := azure.GetToken()
	azure.Client.Header.Add("Authorization", "Bearer "+accessToken)

	res, err := azure.R().SetResult(&response).Get(fmt.Sprintf("/providers/Microsoft.Network/virtualNetworks?api-version=%s", Version))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListFirewalls returns a list of firewalls.
func (azure *AzureManagementClient) ListFirewalls() ([]*v1.Firewall, error) {
	var response v1.FirewallListResult

	accessToken := azure.GetToken()
	azure.Client.Header.Add("Authorization", "Bearer "+accessToken)

	res, err := azure.R().SetResult(&response).Get(fmt.Sprintf("/providers/Microsoft.Network/azureFirewalls?api-version=%s", Version))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}

// ListDatabases returns a list of databases.
func (azure *AzureManagementClient) ListDatabases() ([]*v1.Database, error) {
	//We are only filtering 2 databases here.
	accessToken := azure.GetToken()
	azure.Client.Header.Add("Authorization", "Bearer "+accessToken)
	
	filter := "ResourceType eq 'Microsoft.DBforPostgreSQL/servers' or ResourceType eq 'Microsoft.Sql"
	var response v1.DatabaseListResult
	res, err := azure.R().SetResult(&response).Get(fmt.Sprintf("/resources?$filter=%s/servers/databases'&api-version=2022-11-01-preview", filter))
	if err != nil && res.StatusCode() != http.StatusOK {
		return nil, err
	}
	return response.Value, nil
}
