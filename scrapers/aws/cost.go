package aws

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	athena "github.com/uber/athenadriver/go"
)

const costQueryTemplate = `
    WITH
        max_end_date AS (SELECT MAX(line_item_usage_end_date) as end_date FROM $table WHERE line_item_usage_end_date <= now()
    )

    SELECT DISTINCT
        items.line_item_product_code, items.line_item_resource_id, cost_1h.cost as cost_1h, cost_1d.cost as cost_1d, cost_7d.cost as cost_7d, cost_30d.cost as cost_30d
    FROM $table as items

    FULL JOIN (
        SELECT SUM(line_item_unblended_cost) as cost, line_item_product_code, line_item_resource_id FROM $table
        WHERE line_item_unblended_cost > 0 AND line_item_usage_start_date >= (SELECT date_add('hour', -1, end_date) FROM max_end_date)
        GROUP BY line_item_product_code, line_item_resource_id) AS cost_1h
    ON cost_1h.line_item_product_code = items.line_item_product_code AND items.line_item_resource_id = cost_1h.line_item_resource_id

    FULL JOIN (
        SELECT SUM(line_item_unblended_cost) as cost, line_item_product_code, line_item_resource_id FROM $table
        WHERE line_item_unblended_cost > 0 AND line_item_usage_start_date >= (SELECT date_add('day', -1, end_date) FROM max_end_date)
        GROUP BY line_item_product_code, line_item_resource_id) AS cost_1d
    ON cost_1d.line_item_product_code = items.line_item_product_code AND items.line_item_resource_id = cost_1d.line_item_resource_id

    FULL JOIN (
        SELECT SUM(line_item_unblended_cost) as cost, line_item_product_code, line_item_resource_id FROM $table
        WHERE line_item_unblended_cost > 0 AND line_item_usage_start_date >= (SELECT date_add('day', -7, end_date) FROM max_end_date)
        GROUP BY line_item_product_code, line_item_resource_id) AS cost_7d
    ON cost_7d.line_item_product_code = items.line_item_product_code AND items.line_item_resource_id = cost_7d.line_item_resource_id

    FULL JOIN (
        SELECT SUM(line_item_unblended_cost) as cost, line_item_product_code, line_item_resource_id FROM $table
        WHERE line_item_unblended_cost > 0 AND line_item_usage_start_date >= (SELECT date_add('day', -30, end_date) FROM max_end_date)
        GROUP BY line_item_product_code, line_item_resource_id) AS cost_30d
    ON cost_30d.line_item_product_code = items.line_item_product_code AND items.line_item_resource_id = cost_30d.line_item_resource_id
`

func getAWSAthenaConfig(ctx *v1.ScrapeContext, awsConfig v1.AWS) (*athena.Config, error) {
	conf := athena.NewNoOpsConfig()

	if err := conf.SetRegion(awsConfig.CostReporting.Region); err != nil {
		return nil, err
	}
	if err := conf.SetOutputBucket(awsConfig.CostReporting.S3BucketPath); err != nil {
		return nil, err
	}

	accessKey, secretKey, err := getAccessAndSecretKey(ctx, *awsConfig.AWSConnection)
	if err != nil {
		return nil, err
	}
	if len(accessKey) > 0 && len(secretKey) > 0 {
		if err = conf.SetAccessID(accessKey); err != nil {
			return nil, err
		}
		if err = conf.SetSecretAccessKey(secretKey); err != nil {
			return nil, err
		}
	}
	return conf, nil
}

func fetchCosts(ctx *v1.ScrapeContext, config v1.AWS) ([]v1.LineItem, error) {
	var lineItemRows []v1.LineItem

	athenaConf, err := getAWSAthenaConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get athen conf: %w", err)
	}

	athenaDB, err := sql.Open(athena.DriverName, athenaConf.Stringify())
	if err != nil {
		return nil, fmt.Errorf("failed to open sql connection to %s: %w", athena.DriverName, err)
	}

	table := fmt.Sprintf("%s.%s", config.CostReporting.Database, config.CostReporting.Table)
	query := strings.ReplaceAll(costQueryTemplate, "$table", table)

	rows, err := athenaDB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query athena: %w", err)
	}

	for rows.Next() {
		var productCode, resourceID, cost1h, cost1d, cost7d, cost30d string
		if err := rows.Scan(&productCode, &resourceID, &cost1h, &cost1d, &cost7d, &cost30d); err != nil {
			logger.Errorf("error scanning athena database rows: %v", err)
			continue
		}

		cost1hFloat, _ := strconv.ParseFloat(cost1h, 64)
		cost1dFloat, _ := strconv.ParseFloat(cost1d, 64)
		cost7dFloat, _ := strconv.ParseFloat(cost7d, 64)
		cost30dFloat, _ := strconv.ParseFloat(cost30d, 64)

		lineItemRows = append(lineItemRows, v1.LineItem{
			ExternalID: fmt.Sprintf("%s/%s", productCode, resourceID),
			CostPerMin: cost1hFloat / 60,
			Cost1d:     cost1dFloat,
			Cost7d:     cost7dFloat,
			Cost30d:    cost30dFloat,
		})
	}

	return lineItemRows, nil
}

type CostScraper struct{}

func (awsCost CostScraper) Scrape(ctx *v1.ScrapeContext, config v1.ConfigScraper) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, awsConfig := range config.AWS {
		session, err := NewSession(ctx, *awsConfig.AWSConnection, awsConfig.Region[0])
		if err != nil {
			return results.Errorf(err, "failed to create AWS session")
		}

		stsClient := sts.NewFromConfig(*session)
		caller, err := stsClient.GetCallerIdentity(ctx, nil)
		if err != nil {
			return results.Errorf(err, "failed to get identity")
		}
		accountID := *caller.Account

		rows, err := fetchCosts(ctx, awsConfig)
		if err != nil {
			return results.Errorf(err, "failed to fetch costs")
		}

		results = append(results, v1.ScrapeResult{
			Costs: &v1.CostData{
				LineItems:    rows,
				ExternalType: "AWS::::Account",
				ExternalID:   accountID,
			},
		})
	}

	return results
}
