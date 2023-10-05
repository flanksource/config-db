package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trafficmanager/armtrafficmanager"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

type Scraper struct {
	ctx    context.Context
	cred   *azidentity.ClientSecretCredential
	config *v1.Azure
}

func (azure Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Azure) > 0
}

// HydrateConnection populates the credentials in Azure from the connection name (if available)
// else it'll try to fetch the credentials from kubernetes secrets.
func (azure Scraper) hydrateConnection(ctx api.ScrapeContext, t v1.Azure) error {
	if t.ConnectionName != "" {
		connection, err := ctx.HydrateConnection(t.ConnectionName)
		if err != nil {
			return fmt.Errorf("could not hydrate connection: %w", err)
		} else if connection == nil {
			return fmt.Errorf("connection %s not found", t.ConnectionName)
		}

		t.ClientID.ValueStatic = connection.Username
		t.ClientSecret.ValueStatic = connection.Password
		t.TenantID = connection.Properties["tenant"]
		return nil
	}

	var err error
	t.ClientID.ValueStatic, err = ctx.GetEnvValueFromCache(t.ClientID)
	if err != nil {
		return fmt.Errorf("failed to get client id: %w", err)
	}

	t.ClientSecret.ValueStatic, err = ctx.GetEnvValueFromCache(t.ClientSecret)
	if err != nil {
		return fmt.Errorf("failed to get client secret: %w", err)
	}

	return nil
}

func (azure Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, config := range ctx.ScrapeConfig().Spec.Azure {
		if err := azure.hydrateConnection(ctx, config); err != nil {
			results.Errorf(err, "failed to populate connection")
			continue
		}

		cred, err := azidentity.NewClientSecretCredential(config.TenantID, config.ClientID.ValueStatic, config.ClientSecret.ValueStatic, nil)
		if err != nil {
			results.Errorf(err, "failed to get credentials for azure")
			continue
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
		results = append(results, azure.fetchAppServices()...)
		results = append(results, azure.fetchDNS()...)
		results = append(results, azure.fetchPrivateDNSZones()...)
		results = append(results, azure.fetchTrafficManagerProfiles()...)
		results = append(results, azure.fetchNetworkSecurityGroups()...)
		results = append(results, azure.fetchPublicIPAddresses()...)
		results = append(results, azure.fetchAdvisorAnalysis()...)
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "RelationalDatabase",
				Type:        getARMType(v.Type),
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "KubernetesCluster",
				Type:        getARMType(v.Type),
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "Firewall",
				Type:        getARMType(v.Type),
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "ContainerRegistry",
				Type:        getARMType(v.Type),
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "VirtualNetwork",
				Type:        getARMType(v.Type),
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "LoadBalancer",
				Type:        getARMType(v.Type),
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "VirtualMachine",
				Type:        getARMType(v.Type),
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "ResourceGroup",
				Type:        getARMType(v.Type),
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
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        *v.DisplayName,
				Config:      v,
				ConfigClass: "Subscription",
				Type:        "Azure::SUBSCRIPTION",
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
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate storage account client: %w", err)})
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		respPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read storage account next page: %w", err)})
		}

		for _, v := range respPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "StorageAccount",
				Type:        getARMType(v.Type),
			})
		}
	}

	return results
}

// fetchAppServices gets Azure app services in a subscription.
func (azure Scraper) fetchAppServices() v1.ScrapeResults {
	logger.Debugf("fetching web services for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	client, err := armappservice.NewWebAppsClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate app services client: %w", err)})
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		respPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read app services next page: %w", err)})
		}

		for _, v := range respPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "AppService",
				Type:        getARMType(v.Type),
			})
		}
	}

	return results
}

// fetchDNS gets Azure app services in a subscription.
func (azure Scraper) fetchDNS() v1.ScrapeResults {
	logger.Debugf("fetching dns zones for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	client, err := armdns.NewZonesClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate dns zone client: %w", err)})
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		respPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read dns zone next page: %w", err)})
		}

		for _, v := range respPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "DNSZone",
				Type:        getARMType(v.Type),
			})
		}
	}

	return results
}

// fetchPrivateDNSZones gets Azure app services in a subscription.
func (azure Scraper) fetchPrivateDNSZones() v1.ScrapeResults {
	logger.Debugf("fetching private DNS zones for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	client, err := armprivatedns.NewPrivateZonesClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate private DNS zones client: %w", err)})
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		nextPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read private DNS zones page: %w", err)})
		}

		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "PrivateDNSZone",
				Type:        getARMType(v.Type),
			})
		}
	}

	return results
}

// fetchTrafficManagerProfiles gets traffic manager profiles in a subscription.
func (azure Scraper) fetchTrafficManagerProfiles() v1.ScrapeResults {
	logger.Debugf("fetching traffic manager profiles for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	client, err := armtrafficmanager.NewProfilesClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate traffic manager profile client: %w", err)})
	}

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		respPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read traffic manager profile next page: %w", err)})
		}

		for _, v := range respPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "TrafficManagerProfile",
				Type:        getARMType(v.Type),
			})
		}
	}

	return results
}

// fetchNetworkSecurityGroups gets network security groups in a subscription.
func (azure Scraper) fetchNetworkSecurityGroups() v1.ScrapeResults {
	logger.Debugf("fetching network security groups for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	client, err := armnetwork.NewSecurityGroupsClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate network security groups client: %w", err)})
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		nextPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read network security groups page: %w", err)})
		}

		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "SecurityGroup",
				Type:        getARMType(v.Type),
			})
		}
	}

	return results
}

// fetchPublicIPAddresses gets Azure public IP addresses in a subscription.
func (azure Scraper) fetchPublicIPAddresses() v1.ScrapeResults {
	logger.Debugf("fetching public IP addresses for subscription %s", azure.config.SubscriptionID)

	var results v1.ScrapeResults
	client, err := armnetwork.NewPublicIPAddressesClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to initiate public IP addresses client: %w", err)})
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		nextPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to read public IP addresses page: %w", err)})
		}

		for _, v := range nextPage.Value {
			results = append(results, v1.ScrapeResult{
				BaseScraper: azure.config.BaseScraper,
				ID:          getARMID(v.ID),
				Name:        deref(v.Name),
				Config:      v,
				ConfigClass: "PublicIPAddress",
				Type:        getARMType(v.Type),
			})
		}
	}

	return results
}

// getARMID takes in an ID of a resource group
// and returns it in a compatible format.
func getARMID(id *string) string {
	// Need to lowercase the ID in the config item because
	// the azure advisor recommendation uses resource id in all lowercase; not always but most of the time.
	// This is required to match config analysis with the config item.
	return strings.ToLower(deref(id))
}

// getARMType takes in a type of a resource group
// and returns it in a compatible format.
func getARMType(rType *string) string {
	// Need to uppercase the resource type in the config item because
	// the azure advisor recommendation uses resource type in all uppercase; not always but most of the time.
	// For example: It returns
	// - MICROSOFT.COMPUTE/VIRTUALMACHINES (all caps)
	// - Microsoft.ContainerService/ManagedClusters (case not enforced)
	// This is required to match config analysis with the config item.
	return "Azure::" + strings.ToUpper(deref(rType))
}
