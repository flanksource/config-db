package core

import (
	"context"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
)

type AzureScraper struct {
}

// Scrape ...
func (azure AzureScraper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {

	// Begin scraping.

	results := v1.ScrapeResults{}
	for _, config := range configs.Azure {

		// Build credential. AZURE_CLIENT_ID, AZURE_CLIENT_SECRET and AZURE_TENANT_ID environment variables must be
		//set for this to work.

		ct := context.Background()
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			logger.Fatalf(errors.Verbose(err))
		}

		// Get resource groups in the subscription.
		logger.Debugf("resource groups", "status", "scrape started", config.SubscriptionId)
		resourceGroups := fetchResourceGroups(ct, config, cred)
		results = append(results, resourceGroups...)

		// Get virtual machines in the subscription.
		virtualMachines := fetchVirtualMachines(ct, config, cred)
		results = append(results, virtualMachines...)

		// Get load balancers in the subscription.
		loadBalancers := fetchLoadBalancers(ct, config, cred)
		results = append(results, loadBalancers...)

		// Get virtual networks in the subscription.
		virtualNetworks := fetchVirtualNetworks(ct, config, cred)
		results = append(results, virtualNetworks...)

		// Get container registries in the subscription.
		containerRegistries := fetchContainerRegistries(ct, config, cred)
		results = append(results, containerRegistries...)

		// Get firewalls in the subscription.
		firewalls := fetchFirewalls(ct, config, cred)
		results = append(results, firewalls...)

		// Get K8s managed clusters in the subscription.
		k8s := fetchK8s(ct, config, cred)
		results = append(results, k8s...)

		// Get databases in the subscription.
		databases := fetchDatabases(ct, config, cred)
		results = append(results, databases...)

	}
	return results

}

// fetchDatabases gets all databases in a subscription.
func fetchDatabases(ct context.Context, config v1.Azure, cred *azidentity.DefaultAzureCredential) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	databases, err := armresources.NewClient(config.SubscriptionId, cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate databases client: %w", err))
	}
	options := &armresources.ClientListOptions{
		Filter: to.Ptr("ResourceType eq 'Microsoft.DBforPostgreSQL/servers' or ResourceType eq 'Microsoft.Sql/servers/databases'"),
	}
	dbs := databases.NewListPager(options)
	for dbs.More() {
		nextPage, err := dbs.NextPage(ct)
		if err != nil {
			results = append(results, v1.ScrapeResult{}.Errorf("failed to read databases page: %w", err))
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        *v.Type,
				ID:          *v.ID,
				Name:        *v.Name,
			}.Success(nextPage.Value))
		}
	}
	return results
}

// fetchK8s gets all kubernetes clusters in a subscription.
func fetchK8s(ct context.Context, config v1.Azure, cred *azidentity.DefaultAzureCredential) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	managedClustersClient, err := armcontainerservice.NewManagedClustersClient(config.SubscriptionId, cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate k8s client: %w", err))
	}
	k8sPager := managedClustersClient.NewListPager(nil)
	for k8sPager.More() {
		nextPage, err := k8sPager.NextPage(ct)
		if err != nil {
			results = append(results, v1.ScrapeResult{}.Errorf("failed to read k8s page: %w", err))
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        *v.Type,
				ID:          *v.ID,
				Name:        *v.Name,
			}.Success(nextPage.Value))
		}
	}
	return results
}

// fetchFirewalls gets all firewalls in a subscription.
func fetchFirewalls(ctx context.Context, config v1.Azure, cred *azidentity.DefaultAzureCredential) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	firewallClient, err := armnetwork.NewAzureFirewallsClient(config.SubscriptionId, cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate firewall client: %w", err))
	}
	firewallsPager := firewallClient.NewListAllPager(nil)
	for firewallsPager.More() {
		nextPage, err := firewallsPager.NextPage(ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{}.Errorf("failed to read firewall page: %w", err))
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        *v.Type,
				ID:          *v.ID,
				Name:        *v.Name,
			}.Success(nextPage.Value))
		}
	}
	return results
}

// fetchContainerRegistries gets container registries in a subscription.
func fetchContainerRegistries(ctx context.Context, config v1.Azure, cred *azidentity.DefaultAzureCredential) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	registriesClient, err := armcontainerregistry.NewRegistriesClient(config.SubscriptionId, cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate container registry client: %w", err))
	}
	registriesPager := registriesClient.NewListPager(nil)
	for registriesPager.More() {
		nextPage, err := registriesPager.NextPage(ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{}.Errorf("failed to read container registry page: %w", err))
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        *v.Type,
				ID:          *v.ID,
				Name:        *v.Name,
			}.Success(nextPage.Value))
		}
	}
	return results
}

// fetchVirtualNetworks gets virtual machines in a subscription.
func fetchVirtualNetworks(ctx context.Context, config v1.Azure, cred *azidentity.DefaultAzureCredential) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	virtualNetworksClient, err := armnetwork.NewVirtualNetworksClient(config.SubscriptionId, cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate virtual networks client: %w", err))
	}
	if virtualNetworksClient != nil {
		virtualNetworksPager := virtualNetworksClient.NewListAllPager(nil)
		for virtualNetworksPager.More() {
			nextPage, err := virtualNetworksPager.NextPage(ctx)
			if err != nil {
				results = append(results, v1.ScrapeResult{}.Errorf("failed to read virtual network page: %w", err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				}.Success(nextPage.Value))
			}
		}
	}
	return results
}

// fetchLoadBalancers gets load balancers in a subscription.
func fetchLoadBalancers(ctx context.Context, config v1.Azure, cred *azidentity.DefaultAzureCredential) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	lbClient, err := armnetwork.NewLoadBalancersClient(config.SubscriptionId, cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate load balancer client: %w", err))
	}
	if lbClient != nil {
		loadBalancersPager := lbClient.NewListAllPager(nil)
		for loadBalancersPager.More() {
			nextPage, err := loadBalancersPager.NextPage(ctx)
			if err != nil {
				results = append(results, v1.ScrapeResult{}.Errorf("failed to read load balancer page: %w", err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				}.Success(nextPage.Value))

			}
		}
	}
	return results
}

// fetchVirtualMachines gets virtual machines in a subscription.
func fetchVirtualMachines(ctx context.Context, config v1.Azure, cred *azidentity.DefaultAzureCredential) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	virtualMachineClient, err := armcompute.NewVirtualMachinesClient(config.SubscriptionId, cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate virtual machine client: %w", err))
	}
	if virtualMachineClient != nil {
		virtualMachinePager := virtualMachineClient.NewListAllPager(nil)
		for virtualMachinePager.More() {
			nextPage, err := virtualMachinePager.NextPage(ctx)
			if err != nil {
				results = append(results, v1.ScrapeResult{}.Errorf("failed to read virtual machines page: %w", err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				}.Success(nextPage.Value))
			}
		}
	}
	return results
}

// fetchResourceGroups gets resource groups in a subscription.
func fetchResourceGroups(ctx context.Context, config v1.Azure, cred *azidentity.DefaultAzureCredential) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	resourceClient, err := armresources.NewResourceGroupsClient(config.SubscriptionId, cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate resource group client: %w", err))
	}
	if resourceClient != nil {
		resourcePager := resourceClient.NewListPager(nil)
		for resourcePager.More() {
			nextPage, er := resourcePager.NextPage(ctx)
			if er != nil {
				results = append(results, v1.ScrapeResult{}.Errorf("failed to read resource group page: %w", err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				}.Success(nextPage.Value))
			}
		}
	}
	return results
}
