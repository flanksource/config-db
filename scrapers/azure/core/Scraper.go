package core

import (
	"context"
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

const MicrosoftAuthorityHost = "https://login.microsoftonline.com/"

type AzureScraper struct {
}

// Scrape ...
func (azure AzureScraper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {

	// =========================================================================
	// Begin scraping.

	results := v1.ScrapeResults{}
	for _, config := range configs.Azure {

		// =========================================================================
		// Build credential. AZURE_CLIENT_ID, AZURE_CLIENT_SECRET and AZURE_TENANT_ID environment variables must be
		//set for this to work.

		ct := context.Background()
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			logger.Fatalf(errors.Verbose(err))
		}

		// =========================================================================
		// Get resource groups in the subscription.

		logger.Debugf("resource groups", "status", "scrape started", config.SubscriptionId)
		resourceClient, err := armresources.NewResourceGroupsClient(config.SubscriptionId, cred, nil)
		if err != nil {
			logger.Fatalf(errors.Verbose(err))
		}
		resourcePager := resourceClient.NewListPager(nil)
		for resourcePager.More() {
			nextPage, err := resourcePager.NextPage(ct)
			if err != nil {
				logger.Fatalf(errors.Verbose(err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				})
			}
		}

		// =========================================================================
		// Get virtual machines in the subscription.

		virtualMachineClient, err := armcompute.NewVirtualMachinesClient(config.SubscriptionId, cred, nil)
		if err != nil {
			// handle error
		}
		virtualMachinePager := virtualMachineClient.NewListAllPager(nil)
		for virtualMachinePager.More() {
			nextPage, err := virtualMachinePager.NextPage(ct)
			if err != nil {
				logger.Fatalf(errors.Verbose(err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				})
			}
		}

		// =========================================================================
		// Get load balancers in the subscription.

		lbClient, err := armnetwork.NewLoadBalancersClient(config.SubscriptionId, cred, nil)
		if err != nil {
			logger.Fatalf(errors.Verbose(err))
		}
		loadBalancersPager := lbClient.NewListAllPager(nil)
		for loadBalancersPager.More() {
			nextPage, err := loadBalancersPager.NextPage(ct)
			if err != nil {
				logger.Fatalf(errors.Verbose(err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				})
			}
		}

		// =========================================================================
		// Get virtual networks in the subscription.

		virtualNetworksClient, err := armnetwork.NewVirtualNetworksClient(config.SubscriptionId, cred, nil)
		if err != nil {
			logger.Fatalf(errors.Verbose(err))
		}
		virtualNetworksPager := virtualNetworksClient.NewListAllPager(nil)
		for virtualNetworksPager.More() {
			nextPage, err := virtualNetworksPager.NextPage(ct)
			if err != nil {
				logger.Fatalf(errors.Verbose(err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				})
			}
		}

		// =========================================================================
		// Get container registries in the subscription.

		registriesClient, err := armcontainerregistry.NewRegistriesClient(config.SubscriptionId, cred, nil)
		if err != nil {
			logger.Fatalf(errors.Verbose(err))
		}
		registriesPager := registriesClient.NewListPager(nil)
		for registriesPager.More() {
			nextPage, err := registriesPager.NextPage(ct)
			if err != nil {
				logger.Fatalf(errors.Verbose(err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				})
			}
		}

		// =========================================================================
		// Get firewalls in the subscription.

		firewallClient, err := armnetwork.NewAzureFirewallsClient(config.SubscriptionId, cred, nil)
		if err != nil {
			logger.Fatalf("failed to create client: %v", err)
		}
		firewallsPager := firewallClient.NewListAllPager(nil)
		for firewallsPager.More() {
			nextPage, err := firewallsPager.NextPage(ct)
			if err != nil {
				logger.Fatalf(errors.Verbose(err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				})
			}
		}

		// =========================================================================
		// Get K8s managed clusters in the subscription.

		managedClustersClient, err := armcontainerservice.NewManagedClustersClient(config.SubscriptionId, cred, nil)
		if err != nil {
			logger.Fatalf("failed to create client: %v", err)
		}
		k8sPager := managedClustersClient.NewListPager(nil)
		for k8sPager.More() {
			nextPage, err := k8sPager.NextPage(ct)
			if err != nil {
				logger.Fatalf(errors.Verbose(err))
			}
			for _, v := range nextPage.Value {
				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Type:        *v.Type,
					ID:          *v.ID,
					Name:        *v.Name,
				})
			}
		}
	}
	return results

}
