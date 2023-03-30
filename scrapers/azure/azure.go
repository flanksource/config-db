package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
)

type Scraper struct {
	ctx    context.Context
	cred   *azidentity.ClientSecretCredential
	config *v1.Azure
}

// Scrape ...
func (azure Scraper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, config := range configs.Azure {
		cred, err := azidentity.NewClientSecretCredential(config.TenantID, config.ClientID.Value, config.ClientSecret.Value, nil)
		if err != nil {
			logger.Fatalf(errors.Verbose(err))
		}

		azure.ctx = context.Background()
		azure.config = &config
		azure.cred = cred

		results = append(results, azure.fetchResourceGroups()...)
		results = append(results, azure.fetchVirtualMachines()...)
		results = append(results, azure.fetchLoadBalancers()...)
		results = append(results, azure.fetchVirtualNetworks()...)
		results = append(results, azure.fetchContainerRegistries()...)
		results = append(results, azure.fetchFirewalls()...)
		results = append(results, azure.fetchDatabases()...)
		results = append(results, azure.fetchK8s()...)
		results = append(results, azure.fetchSubscriptions()...)
		results = append(results, azure.fetchStorageAccounts()...)
	}

	return results
}

// fetchDatabases gets all databases in a subscription.
func (azure Scraper) fetchDatabases() v1.ScrapeResults {
	logger.Debugf("fetching databases for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	databases, err := armresources.NewClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate database client: %w", err)})
	}
	options := &armresources.ClientListOptions{
		Expand: nil,
		Filter: to.Ptr(`
            ResourceType eq 'Microsoft.DBforPostgreSQL/servers' or
            ResourceType eq 'Microsoft.Sql/servers/databases'
        `),
	}
	dbs := databases.NewListPager(options)
	for dbs.More() {
		nextPage, err := dbs.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read database page: %w", err)})
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Config:       v,
				Type:         "RelationalDatabase",
				ExternalType: *v.Type,
			})
		}
	}
	return results
}

// fetchK8s gets all kubernetes clusters in a subscription.
func (azure Scraper) fetchK8s() v1.ScrapeResults {
	logger.Debugf("fetching k8s for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	managedClustersClient, err := armcontainerservice.NewManagedClustersClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate k8s client: %w", err)})
	}

	k8sPager := managedClustersClient.NewListPager(nil)
	for k8sPager.More() {
		nextPage, err := k8sPager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read k8s page: %w", err)})
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Config:       v,
				Type:         "KubernetesCluster",
				ExternalType: *v.Type,
			})
		}
	}
	return results
}

// fetchFirewalls gets all firewalls in a subscription.
func (azure Scraper) fetchFirewalls() v1.ScrapeResults {
	logger.Debugf("fetching firewalls for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	firewallClient, err := armnetwork.NewAzureFirewallsClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate firewall client: %w", err)})
	}

	firewallsPager := firewallClient.NewListAllPager(nil)
	for firewallsPager.More() {
		nextPage, err := firewallsPager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read firewall page: %w", err)})
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Config:       v,
				Type:         "Firewall",
				ExternalType: *v.Type,
			})
		}
	}
	return results
}

// fetchContainerRegistries gets container registries in a subscription.
func (azure Scraper) fetchContainerRegistries() v1.ScrapeResults {
	logger.Debugf("fetching container registries for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	registriesClient, err := armcontainerregistry.NewRegistriesClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate container registries client: %w", err)})
	}
	registriesPager := registriesClient.NewListPager(nil)
	for registriesPager.More() {
		nextPage, err := registriesPager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read container registries page: %w", err)})
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Config:       v,
				Type:         "ContainerRegistry",
				ExternalType: *v.Type,
			})
		}
	}
	return results
}

// fetchVirtualNetworks gets virtual machines in a subscription.
func (azure Scraper) fetchVirtualNetworks() v1.ScrapeResults {
	logger.Debugf("fetching virtual networks for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	virtualNetworksClient, err := armnetwork.NewVirtualNetworksClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate virtual network client: %w", err)})
	}

	virtualNetworksPager := virtualNetworksClient.NewListAllPager(nil)
	for virtualNetworksPager.More() {
		nextPage, err := virtualNetworksPager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read virtual network page: %w", err)})
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Config:       v,
				Type:         "VirtualNetwork",
				ExternalType: *v.Type,
			})
		}
	}
	return results
}

// fetchLoadBalancers gets load balancers in a subscription.
func (azure Scraper) fetchLoadBalancers() v1.ScrapeResults {
	logger.Debugf("fetching load balancers for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	lbClient, err := armnetwork.NewLoadBalancersClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate load balancer client: %w", err)})
	}

	loadBalancersPager := lbClient.NewListAllPager(nil)
	for loadBalancersPager.More() {
		nextPage, err := loadBalancersPager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read load balancer page: %w", err)})
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Config:       v,
				Type:         "LoadBalancer",
				ExternalType: *v.Type,
			})

		}
	}
	return results
}

// fetchVirtualMachines gets virtual machines in a subscription.
func (azure Scraper) fetchVirtualMachines() v1.ScrapeResults {
	logger.Debugf("fetching virtual machines for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	virtualMachineClient, err := armcompute.NewVirtualMachinesClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate virtual machine client: %w", err)})
	}

	virtualMachinePager := virtualMachineClient.NewListAllPager(nil)
	for virtualMachinePager.More() {
		nextPage, err := virtualMachinePager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed read virtual machine page: %w", err)})
		}
		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Config:       v,
				Type:         "VirtualMachine",
				ExternalType: *v.Type,
			})
		}
	}
	return results
}

// fetchResourceGroups gets resource groups in a subscription.
func (azure Scraper) fetchResourceGroups() v1.ScrapeResults {
	logger.Debugf("fetching resource groups for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	resourceClient, err := armresources.NewResourceGroupsClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate resource group client: %w", err)})
	}

	resourcePager := resourceClient.NewListPager(nil)
	for resourcePager.More() {
		nextPage, err := resourcePager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed reading resource group page: %w", err)})
		}

		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Type:         "ResourceGroup",
				ExternalType: *v.Type,
			})
		}
	}
	return results
}

// fetchSubscriptions gets Azure subscriptions.
func (azure Scraper) fetchSubscriptions() v1.ScrapeResults {
	logger.Debugf("fetching subscriptions")
	var results v1.ScrapeResults

	client, err := armsubscription.NewSubscriptionsClient(azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate subscriptions client: %w", err)})
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		respPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read subscription next page: %w", err)})
		}

		for _, v := range respPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.DisplayName,
				Config:       v,
				Type:         "Subscription",
				ExternalType: "Subscription",
			})
		}
	}

	return results
}

// fetchStorageAccounts gets storage accounts in a subscription.
func (azure Scraper) fetchStorageAccounts() v1.ScrapeResults {
	logger.Debugf("fetching storage accounts for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults

	client, err := armstorage.NewAccountsClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate resource group client: %w", err)})
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		respPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read storage account next page: %w", err)})
		}
		for _, v := range respPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper:  azure.config.BaseScraper,
				ID:           *v.ID,
				Name:         *v.Name,
				Config:       v,
				Type:         "StorageAccount",
				ExternalType: *v.Type,
			})
		}
	}

	return results
}
