package aws

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	athena "github.com/uber/athenadriver/go"
)

const hourlyQueryTemplate = `
    SELECT sum(cost) FROM $table
    WHERE line_item_resource_id = @id AND line_item_product_code = @product_code AND line_item_usage_end_date = (
        SELECT MAX(line_item_usage_end_date) FROM $table
        WHERE line_item_resource_id = @id AND line_item_product_code = @product_code
    );
`

const dayQueryTemplate = `
    WITH max_end_date AS (
        SELECT MAX(line_item_usage_end_date) as end_date FROM $table
        WHERE line_item_resource_id = @id AND line_item_product_code = @product_code
    )
    SELECT sum(cost) FROM $table
    WHERE line_item_resource_id = @id AND line_item_product_code = @product_code AND
        line_item_usage_end_date = (SELECT end_date FROM max_end_date) AND
        line_item_usage_start_date >= (SELECT date_add('day', -$days, end_date) FROM max_end_date)
`

func getJSONKey(body, key string) (interface{}, error) {
	var j map[string]interface{}
	if err := json.Unmarshal([]byte(body), &j); err != nil {
		return nil, err
	}
	return j[key], nil
}

type productAttributes struct {
	ResourceID  string
	ProductCode string
}

func getProductAttributes(ci models.ConfigItem) (productAttributes, error) {
	var resourceID, productCode string

	switch *ci.ExternalType {
	case v1.AWSEC2Instance:
		resourceID = *ci.Name
		productCode = "AmazonEC2"

	case v1.AWSEKSCluster:
		arn, err := getJSONKey(*ci.Config, "arn")
		if err != nil {
			return productAttributes{}, err
		}
		resourceID = arn.(string)
		productCode = "AmazonEKS"

	case v1.AWSS3Bucket:
		resourceID = *ci.Name
		productCode = "AmazonS3"

	case v1.AWSLoadBalancer:
		resourceID = fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", *ci.Region, *ci.Account, *ci.Name)
		productCode = "AWSELB"

	case v1.AWSLoadBalancerV2:
		resourceID = ci.ExternalID[0]
		// TODO: Check
		productCode = "AWSELBV2"

	case v1.AWSEBSVolume:
		resourceID = *ci.Name
		productCode = "AmazonEC2"

	case v1.AWSRDSInstance:
		// TODO: Check
		resourceID = ci.ExternalID[0]
		productCode = "AmazonRDS"
	}

	return productAttributes{
		ResourceID:  resourceID,
		ProductCode: productCode,
	}, nil
}

func getAWSAthenaConfig(ctx *v1.ScrapeContext, awsConfig v1.AWS) (*athena.Config, error) {
	accessKey, secretKey, err := getAccessAndSecretKey(ctx, *awsConfig.AWSConnection)
	if err != nil {
		return nil, err
	}
	conf, err := athena.NewDefaultConfig(awsConfig.CostReporting.S3BucketPath, awsConfig.CostReporting.Region, accessKey, secretKey)
	return conf, err
}

type periodicCosts struct {
	Hourly  float64
	Daily   float64
	Weekly  float64
	Monthly float64
}

func FetchCosts(ctx *v1.ScrapeContext, config v1.AWS, ci models.ConfigItem) (periodicCosts, error) {
	attrs, err := getProductAttributes(ci)
	if err != nil {
		return periodicCosts{}, err
	}

	athenaConf, err := getAWSAthenaConfig(ctx, config)
	if err != nil {
		return periodicCosts{}, err
	}

	athenaDB, err := sql.Open(athena.DriverName, athenaConf.Stringify())
	if err != nil {
		return periodicCosts{}, err
	}

	table := fmt.Sprintf("%s.%s", config.CostReporting.Database, config.CostReporting.Table)

	queryArgs := []interface{}{sql.Named("id", attrs.ResourceID), sql.Named("product_code", attrs.ProductCode)}

	var hourlyCost float64
	hourlyQuery := strings.ReplaceAll(hourlyQueryTemplate, "$table", table)
	if err = athenaDB.QueryRow(hourlyQuery, queryArgs...).Scan(&hourlyCost); err != nil {
		return periodicCosts{}, err
	}

	var dailyCost float64
	dailyQuery := strings.ReplaceAll(strings.ReplaceAll(dayQueryTemplate, "$table", table), "$days", "1")
	if err = athenaDB.QueryRow(dailyQuery, queryArgs...).Scan(&dailyCost); err != nil {
		return periodicCosts{}, err
	}

	var weeklyCost float64
	weeklyQuery := strings.ReplaceAll(strings.ReplaceAll(dayQueryTemplate, "$table", table), "$days", "7")
	if err = athenaDB.QueryRow(weeklyQuery, queryArgs...).Scan(&weeklyCost); err != nil {
		return periodicCosts{}, err
	}

	var monthlyCost float64
	monthlyQuery := strings.ReplaceAll(strings.ReplaceAll(dayQueryTemplate, "$table", table), "$days", "30")
	if err = athenaDB.QueryRow(monthlyQuery, queryArgs...).Scan(&monthlyCost); err != nil {
		return periodicCosts{}, err
	}

	return periodicCosts{
		Hourly:  hourlyCost,
		Daily:   dailyCost,
		Weekly:  weeklyCost,
		Monthly: monthlyCost,
	}, nil
}

type CostScraper struct{}

func (awsCost CostScraper) Scrape(ctx v1.ScrapeContext, config v1.ConfigScraper, region string) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, awsConfig := range config.AWS {
		session, err := NewSession(&ctx, *awsConfig.AWSConnection, region)
		if err != nil {
			return results.Errorf(err, "failed to create AWS session")
		}
		STS := sts.NewFromConfig(*session)
		caller, err := STS.GetCallerIdentity(ctx, nil)
		if err != nil {
			return results.Errorf(err, "failed to get identity")
		}
		accountID := *caller.Account

		// fetch config items which match aws resources and account
		configItems, err := db.QueryAWSResources(accountID)
		if err != nil {
			return results.Errorf(err, "failed to query config items from db")
		}

		for _, configItem := range configItems {
			costs, err := FetchCosts(&ctx, awsConfig, configItem)
			if err != nil {
				// TODO Log
			}
			results = append(results, v1.ScrapeResult{
				ID:            configItem.ID,
				CostPerMinute: costs.Hourly / 60,
				CostTotal1d:   costs.Daily,
				CostTotal7d:   costs.Weekly,
				CostTotal30d:  costs.Monthly,
			})
		}

	}

	return results
}

/*
Get latest line_item_usage_start_date - line_item_usage_end_date where cost > 0
where line_item_resource_id = ? and line_item_product_code = ? (AmazonS3, AmazonEC2)
group by
line_item_usage_end_date


Hourly cost of EC2 instance -> select sum(cost) from flanksource_report where line_item_resource_id = 'i-08b0205c8f05f0afd' and line_item_usage_end_date = (select max(line_item_usage_end_date) from flanksource_report where line_item_resource_id = 'i-08b0205c8f05f0afd')
Hourly cost of S3 -> select * from flanksource_report where line_item_resource_id = 'config-bucket-745897381572' and line_item_usage_end_date = (select max(line_item_usage_end_date) from flanksource_report where line_item_resource_id = 'config-bucket-745897381572')
*/
