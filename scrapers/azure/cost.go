package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty"
)

type CostScraper struct {
	cred *azidentity.ClientSecretCredential
}

func (t CostScraper) CanScrape(config v1.ConfigScraper) bool {
	// At least one of the azure configuration must have the subscription ID set
	for _, c := range config.Azure {
		if c.SubscriptionID != "" {
			return true
		}
	}

	return false
}

func (t CostScraper) Scrape(ctx *v1.ScrapeContext, config v1.ConfigScraper) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, config := range config.Azure {
		clientId, err := duty.GetEnvValueFromCache(ctx.Kubernetes, config.ClientID, ctx.Namespace)
		if err != nil {
			results.Errorf(err, "failed to get client id")
			continue
		}

		clientSecret, err := duty.GetEnvValueFromCache(ctx.Kubernetes, config.ClientSecret, ctx.Namespace)
		if err != nil {
			results.Errorf(err, "failed to get client secret")
			continue
		}

		cred, err := azidentity.NewClientSecretCredential(config.TenantID, clientId, clientSecret, nil)
		if err != nil {
			results.Errorf(err, "failed to get credentials for azure")
			continue
		}
		t.cred = cred

		if err := t.GetCost(ctx.Context, config.SubscriptionID); err != nil {
			results.Errorf(err, "failed to get cost")
			continue
		}
	}

	return results
}

func (t *CostScraper) GetCost(ctx context.Context, subscriptionID string) error {
	costClient, err := armcostmanagement.NewQueryClient(t.cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create cost client: %w", err)
	}

	var (
		scope       = fmt.Sprintf("/subscriptions/%s", subscriptionID)
		timeFrame   = armcostmanagement.TimeframeTypeTheLastBillingMonth
		queryType   = armcostmanagement.ExportTypeActualCost
		granularity = armcostmanagement.GranularityTypeDaily
	)
	queryDef := armcostmanagement.QueryDefinition{
		Dataset: &armcostmanagement.QueryDataset{
			Granularity: &granularity,
		},
		Timeframe: &timeFrame,
		Type:      &queryType,
	}
	usageRes, err := costClient.Usage(ctx, scope, queryDef, nil)
	if err != nil {
		return fmt.Errorf("failed to get usage: %w", err)
	}

	logger.Debugf("usage: %v", usageRes)

	return nil
}
