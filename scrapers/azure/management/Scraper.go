package management

import (
	"fmt"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/azure/logger"
	"os"
)

const MicrosoftAuthorityHost = "https://login.microsoftonline.com/"

type AzureManagementScraper struct {
}

// Scrape ...
func (azure AzureManagementScraper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {

	// =========================================================================
	// Build the logger.

	log, err := logger.New("AZURE-MANAGEMENT-SCRAPER")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	results := v1.ScrapeResults{}
	for _, config := range configs.AzureManagement {
		subscriptionId := os.Getenv("AZURE_SUBSCRIPTION_ID")
		// =========================================================================
		// Build Azure management client.

		baseUrl := "https://management.azure.com/subscriptions/{subscriptionId}"
		client, err := NewAzureManagementClient(ctx, log, subscriptionId, baseUrl)
		if err != nil {
			results.Errorf(err, "failed to create azure management client for %s", subscriptionId)
			continue
		}

		// =========================================================================
		// Get resource groups in the subscription.

		log.Infow("resource groups, load balances and virtual machines", "status", "scrape started")
		accessToken := client.GetToken()
		resourceGroups, er := client.ListResourceGroups(accessToken)
		if er != nil {
			results.Errorf(err, "failed to get resource groups for %s", subscriptionId)
			continue
		}
		for _, resourceGroup := range resourceGroups {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        resourceGroup.Type,
				ID:          resourceGroup.ID,
				Tags:        resourceGroup.Tags,
				Name:        resourceGroup.Name,
			})

			// =========================================================================
			// Get load balancers in this resource group.
			accessToken = client.GetToken()
			loadBalancers, er := client.ListLoadBalancers(accessToken, resourceGroup.Name)
			if er != nil {
				results.Errorf(err, "failed to get load balancers for %s", subscriptionId)
				continue
			}
			for _, loadBalancer := range loadBalancers {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        loadBalancer.Type,
					ID:          loadBalancer.ID,
					Tags:        loadBalancer.Tags,
					Name:        loadBalancer.Name,
				})
			}

			// =========================================================================
			// Get load virtual machines in this resource group.
			accessToken = client.GetToken()
			virtualMachines, er := client.ListVirtualMachines(accessToken, resourceGroup.Name)
			if er != nil {
				results.Errorf(err, "failed to get load balancers for %s", subscriptionId)
				continue
			}
			for _, virtualMachine := range virtualMachines {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        virtualMachine.Type,
					ID:          virtualMachine.ID,
					Name:        virtualMachine.Name,
				})
			}
		}
		log.Infow("resource groups, load balances and virtual machines", "status", "scrape complete")

		// =========================================================================
		// Get kubernetes clusters in the subscription.
		log.Infow("kubernetes", "status", "scrape started")
		accessToken = client.GetToken()
		k8Clusters, er := client.ListKubernetesClusters(accessToken)
		if er != nil {
			results.Errorf(err, "failed to get kubernetes clusters for %s", subscriptionId)
			continue
		}
		for _, k8Cluster := range k8Clusters {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        k8Cluster.Type,
				ID:          k8Cluster.ID,
				Name:        k8Cluster.Name,
				Tags:        k8Cluster.Tags,
			})
		}
		log.Infow("kubernetes", "status", "scrape complete")

		// =========================================================================
		// Get Container registries in the subscription.
		log.Infow("container registries", "status", "scrape started")
		accessToken = client.GetToken()
		containerRegistries, er := client.ListContainerRegistries(accessToken)
		if er != nil {
			results.Errorf(err, "failed to get container registries for %s", subscriptionId)
			continue
		}
		for _, containerRegistry := range containerRegistries {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        containerRegistry.Type,
				ID:          containerRegistry.ID,
				Name:        containerRegistry.Name,
				Tags:        containerRegistry.Tags,
			})
		}
		log.Infow("container registries", "status", "scrape complete")

		// =========================================================================
		// Get Virtual networks in the subscription.

		log.Infow("virtual networks", "status", "scrape started")
		accessToken = client.GetToken()
		virtualNetworks, er := client.ListVirtualNetworks(accessToken)
		if er != nil {
			results.Errorf(err, "failed to get virtual networks for %s", subscriptionId)
			continue
		}
		for _, virtualNetwork := range virtualNetworks {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        virtualNetwork.Type,
				ID:          virtualNetwork.ID,
				Name:        virtualNetwork.Name,
			})
		}
		log.Infow("virtual networks", "status", "scrape complete")

		// =========================================================================
		// Get firewalls in the subscription.

		log.Infow("firewalls", "status", "scrape started")
		accessToken = client.GetToken()
		firewalls, er := client.ListFirewalls(accessToken)
		if er != nil {
			results.Errorf(err, "failed to get firewalls for %s", subscriptionId)
			continue
		}
		for _, firewall := range firewalls {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        firewall.Type,
				ID:          firewall.ID,
				Name:        firewall.Name,
				Tags:        firewall.Tags,
			})
		}
		log.Infow("firewalls", "status", "scrape complete")

		// =========================================================================
		// Get databases in the subscription.

		log.Infow("databases", "status", "scrape started")
		accessToken = client.GetToken()
		databases, er := client.ListDatabases(accessToken)
		if er != nil {
			results.Errorf(err, "failed to get firewalls for %s", subscriptionId)
			continue
		}
		for _, database := range databases {
			results = append(results, v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Type:        database.Type,
				ID:          database.ID,
				Name:        database.Name,
				Tags:        database.Tags,
			})
		}
		log.Infow("databases", "status", "scrape complete")
	}
	return results

}
