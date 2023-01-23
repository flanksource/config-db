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
	ctx    context.Context
	cred   *azidentity.DefaultAzureCredential
	config v1.Azure
}

// Scrape ...
func (azure AzureScraper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {

	// Begin scraping.

	results := v1.ScrapeResults{}
	for _, config := range configs.Azure {
		logger.Debugf("azure scraper", "status", "started", config.SubscriptionId)
		// Build credential. AZURE_CLIENT_ID, AZURE_CLIENT_SECRET and AZURE_TENANT_ID environment variables must be
		//set for this to work.

		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			logger.Fatalf(errors.Verbose(err))
		}

		azure.ctx = context.Background()
		azure.config = config
		azure.cred = cred

		// Get resource groups in the subscription.
		resourceGroups := azure.fetchResourceGroups()
		results = append(results, resourceGroups...)

		// Get virtual machines in the subscription.
		virtualMachines := azure.fetchVirtualMachines()
		results = append(results, virtualMachines...)

		// Get load balancers in the subscription.
		loadBalancers := azure.fetchLoadBalancers()
		results = append(results, loadBalancers...)

		// Get virtual networks in the subscription.
		virtualNetworks := azure.fetchVirtualNetworks()
		results = append(results, virtualNetworks...)

		// Get container registries in the subscription.
		containerRegistries := azure.fetchContainerRegistries()
		results = append(results, containerRegistries...)

		// Get firewalls in the subscription.
		firewalls := azure.fetchFirewalls()
		results = append(results, firewalls...)

		// Get K8s managed clusters in the subscription.
		k8s := azure.fetchK8s()
		results = append(results, k8s...)

		// Get databases in the subscription.
		databases := azure.fetchDatabases()
		results = append(results, databases...)

		logger.Debugf("azure scraper", "status", "complete", config.SubscriptionId)

	}
	return results

}

// fetchDatabases gets all databases in a subscription.
func (azure AzureScraper) fetchDatabases() v1.ScrapeResults {
	logger.Debugf("databases scraper", "status", "started", azure.config.SubscriptionId)
	defer logger.Debugf("databases scraper", "status", "complete", azure.config.SubscriptionId)

	results := v1.ScrapeResults{}

	databases, err := armresources.NewClient(azure.config.SubscriptionId, azure.cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate databases client: %w", err))
	}
	options := &armresources.ClientListOptions{
		Filter: to.Ptr("ResourceType eq 'Microsoft.DBforPostgreSQL/servers' or ResourceType eq 'Microsoft.Sql/servers/databases'"),
	}
	dbs := databases.NewListPager(options)
	for dbs.More() {
		nextPage, err := dbs.NextPage(azure.ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{}.Errorf("failed to read databases page: %w", err))
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				Type:        *v.Type,
				ID:          *v.ID,
				Name:        *v.Name,
			}.Success(nextPage.Value))
		}
	}
	return results
}

// fetchK8s gets all kubernetes clusters in a subscription.
func (azure AzureScraper) fetchK8s() v1.ScrapeResults {
	logger.Debugf("k8s scraper", "status", "started", azure.config.SubscriptionId)
	defer logger.Debugf("k8s scraper", "status", "complete", azure.config.SubscriptionId)

	results := v1.ScrapeResults{}

	managedClustersClient, err := armcontainerservice.NewManagedClustersClient(azure.config.SubscriptionId, azure.cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate k8s client: %w", err))
	}
	k8sPager := managedClustersClient.NewListPager(nil)
	for k8sPager.More() {
		nextPage, err := k8sPager.NextPage(azure.ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{}.Errorf("failed to read k8s page: %w", err))
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				Type:        *v.Type,
				ID:          *v.ID,
				Name:        *v.Name,
			}.Success(nextPage.Value))
		}
	}
	return results
}

// fetchFirewalls gets all firewalls in a subscription.
func (azure AzureScraper) fetchFirewalls() v1.ScrapeResults {
	logger.Debugf("firewalls scraper", "status", "started", azure.config.SubscriptionId)
	defer logger.Debugf("firewalls scraper", "status", "complete", azure.config.SubscriptionId)

	results := v1.ScrapeResults{}

	firewallClient, err := armnetwork.NewAzureFirewallsClient(azure.config.SubscriptionId, azure.cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate firewall client: %w", err))
	}
	firewallsPager := firewallClient.NewListAllPager(nil)
	for firewallsPager.More() {
		nextPage, err := firewallsPager.NextPage(azure.ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{}.Errorf("failed to read firewall page: %w", err))
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				Type:        *v.Type,
				ID:          *v.ID,
				Name:        *v.Name,
			}.Success(nextPage.Value))
		}
	}
	return results
}

// fetchContainerRegistries gets container registries in a subscription.
func (azure AzureScraper) fetchContainerRegistries() v1.ScrapeResults {
	logger.Debugf("container registries scraper", "status", "started", azure.config.SubscriptionId)
	defer logger.Debugf("container registries scraper", "status", "complete", azure.config.SubscriptionId)

	results := v1.ScrapeResults{}

	registriesClient, err := armcontainerregistry.NewRegistriesClient(azure.config.SubscriptionId, azure.cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate container registry client: %w", err))
	}
	registriesPager := registriesClient.NewListPager(nil)
	for registriesPager.More() {
		nextPage, err := registriesPager.NextPage(azure.ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{}.Errorf("failed to read container registry page: %w", err))
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				Type:        *v.Type,
				ID:          *v.ID,
				Name:        *v.Name,
			}.Success(nextPage.Value))
		}
	}
	return results
}

// fetchVirtualNetworks gets virtual machines in a subscription.
func (azure AzureScraper) fetchVirtualNetworks() v1.ScrapeResults {
	logger.Debugf("virtual networks scraper", "status", "started", azure.config.SubscriptionId)
	defer logger.Debugf("virtual networks scraper", "status", "complete", azure.config.SubscriptionId)

	results := v1.ScrapeResults{}

	virtualNetworksClient, err := armnetwork.NewVirtualNetworksClient(azure.config.SubscriptionId, azure.cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate virtual networks client: %w", err))
	}
	if virtualNetworksClient != nil {
		virtualNetworksPager := virtualNetworksClient.NewListAllPager(nil)
		for virtualNetworksPager.More() {
			nextPage, err := virtualNetworksPager.NextPage(azure.ctx)
			if err != nil {
				results = append(results, v1.ScrapeResult{}.Errorf("failed to read virtual network page: %w", err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: azure.config.BaseScraper,
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
func (azure AzureScraper) fetchLoadBalancers() v1.ScrapeResults {
	logger.Debugf("load balancers scraper", "status", "started", azure.config.SubscriptionId)
	defer logger.Debugf("load balancers scraper", "status", "complete", azure.config.SubscriptionId)

	results := v1.ScrapeResults{}

	lbClient, err := armnetwork.NewLoadBalancersClient(azure.config.SubscriptionId, azure.cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate load balancer client: %w", err))
	}
	if lbClient != nil {
		loadBalancersPager := lbClient.NewListAllPager(nil)
		for loadBalancersPager.More() {
			nextPage, err := loadBalancersPager.NextPage(azure.ctx)
			if err != nil {
				results = append(results, v1.ScrapeResult{}.Errorf("failed to read load balancer page: %w", err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: azure.config.BaseScraper,
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
func (azure AzureScraper) fetchVirtualMachines() v1.ScrapeResults {
	logger.Debugf("virtual machines scraper", "status", "started", azure.config.SubscriptionId)
	defer logger.Debugf("virtual machines scraper", "status", "complete", azure.config.SubscriptionId)

	results := v1.ScrapeResults{}

	virtualMachineClient, err := armcompute.NewVirtualMachinesClient(azure.config.SubscriptionId, azure.cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate virtual machine client: %w", err))
	}
	if virtualMachineClient != nil {
		virtualMachinePager := virtualMachineClient.NewListAllPager(nil)
		for virtualMachinePager.More() {
			nextPage, err := virtualMachinePager.NextPage(azure.ctx)
			if err != nil {
				results = append(results, v1.ScrapeResult{}.Errorf("failed to read virtual machines page: %w", err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: azure.config.BaseScraper,
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
func (azure AzureScraper) fetchResourceGroups() v1.ScrapeResults {
	logger.Debugf("resource groups scraper", "status", "started", azure.config.SubscriptionId)
	defer logger.Debugf("resource groups scraper", "status", "complete", azure.config.SubscriptionId)

	results := v1.ScrapeResults{}

	resourceClient, err := armresources.NewResourceGroupsClient(azure.config.SubscriptionId, azure.cred, nil)
	if err != nil {
		results = append(results, v1.ScrapeResult{}.Errorf("failed to initiate resource group client: %w", err))
	}
	if resourceClient != nil {
		resourcePager := resourceClient.NewListPager(nil)
		for resourcePager.More() {
			nextPage, er := resourcePager.NextPage(azure.ctx)
			if er != nil {
				results = append(results, v1.ScrapeResult{}.Errorf("failed to read resource group page: %w", err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: azure.config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				}.Success(nextPage.Value))
			}
		}
	}
	return results
}
