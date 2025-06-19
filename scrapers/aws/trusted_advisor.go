package aws

import (
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/support"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
)

const TrustedAdvisorCheckDefaultInterval = time.Hour * 16

func mapCategoryToAnalysisType(category string) models.AnalysisType {
	switch category {
	case "cost_optimizing", "cost":
		return models.AnalysisTypeCost
	case "performance":
		return models.AnalysisTypePerformance
	case "fault_tolerance":
		return models.AnalysisTypeReliability
	case "recommendation":
		return models.AnalysisTypeRecommendation
	default:
		return models.AnalysisTypeOther
	}
}

func mapSeverity(severity string) models.Severity {
	switch severity {
	case "Red":
		return models.SeverityCritical
	case "Yellow":
		return models.SeverityLow
	}

	return models.SeverityInfo
}

func lastTrustedAdvisorCheck(ctx *AWSContext) (*time.Time, error) {
	query := `SELECT details->>'trusted_advisor' AS lastRan 
	FROM job_history 
	WHERE name = 'Scraper' 
		AND resource_id = ? 
		AND details->'trusted_advisor' IS NOT NULL 
	ORDER BY time_start DESC
	LIMIT 1
	`

	var lastRan string
	if err := ctx.DB().Raw(query, ctx.ScraperID()).Scan(&lastRan).Error; err != nil {
		return nil, ctx.Oops().Wrapf(err, "failed to get last trusted advisor check")
	}

	if lastRan == "" {
		return nil, nil
	}

	lastRanTime, err := time.Parse(time.RFC3339, lastRan)
	if err != nil {
		return nil, err
	}

	return &lastRanTime, nil
}

func (aws Scraper) trustedAdvisor(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if config.Excludes("trusted_advisor") {
		return
	}

	checkInterval := ctx.Properties().Duration("scraper.aws.trusted_advisor.minInterval", TrustedAdvisorCheckDefaultInterval)
	if lastRan, err := lastTrustedAdvisorCheck(ctx); err != nil {
		results.Errorf(err, "failed to get last trusted advisor check")
	} else if lastRan != nil && time.Since(*lastRan) < checkInterval {
		logger.Infof("skipping trusted advisor check as it was run %s ago", time.Since(*lastRan))
		return
	}

	trustAdvidorChecksDescribeInput := &support.DescribeTrustedAdvisorChecksInput{
		Language: strPtr("en"),
	}
	trustAdvidorChecksDescribeOutput, err := ctx.Support.DescribeTrustedAdvisorChecks(ctx, trustAdvidorChecksDescribeInput)
	if err != nil {
		results.Errorf(err, "Failed to describe trusted advisor checks")
		return
	}

	// Keep track of a successful trusted advisor check so we can skip the next check if it was run less than min interval
	ctx.JobHistory().AddDetails("trusted_advisor", time.Now().Format(time.RFC3339))

	for _, check := range trustAdvidorChecksDescribeOutput.Checks {
		if config.Excludes(*check.Name) {
			logger.Tracef("Skipping check %s", *check.Name)
			continue
		}
		checks, err := ctx.Support.DescribeTrustedAdvisorCheckResult(ctx, &support.DescribeTrustedAdvisorCheckResultInput{
			Language: strPtr("en"),
			CheckId:  check.Id,
		})
		if err != nil {
			results.Errorf(err, "Failed to describe trusted advisor check result")
			return
		}
		if len(checks.Result.FlaggedResources) == 0 {
			continue
		}
		for _, resource := range checks.Result.FlaggedResources {
			if *resource.Status == "ok" {
				continue
			}

			metadata, id := getMetadata(check.Metadata, resource.Metadata)
			configType := getConfigTypeById(id)
			if configType == "" {
				if metadata["Bucket Name"] != "" {
					id = metadata["Bucket Name"]
					delete(metadata, "Bucket Name")
					configType = "AWS::S3::Bucket"
				} else if metadata["IAM User"] != "" {
					id = metadata["IAM User"]
					delete(metadata, "IAM User")
					configType = "AWS::IAM::User"
				} else if metadata["Hosted Zone Name"] != "" {
					id = metadata["Hosted Zone Name"]
					configType = "AWS::Route53::HostedZone"
					delete(metadata, "Hosted Zone Name")
				} else if metadata["User Name (IAM or Root)"] != "" {
					id = metadata["User Name (IAM or Root)"]
					configType = "AWS::IAM::User"
					delete(metadata, "User Name (IAM or Root)")
				} else if metadata["Region"] != "" {
					id = metadata["Region"]
					configType = v1.AWSRegion
					delete(metadata, "Region")
				} else {
					id = *ctx.Caller.Account
					configType = "AWS::::Account"
				}
			}

			analysis := results.Analysis(*check.Name, configType, id)
			analysis.Status = models.AnalysisStatusOpen
			analysis.AnalysisType = mapCategoryToAnalysisType(*check.Category)
			analysis.Severity = mapSeverity(metadata["Status"])
			delete(metadata, "Status")
			analysis.Message(deref(check.Description))
			analysis.Source = "AWS Trusted Advisor"

			if _analysis, err := utils.ToJSONMap(metadata); err == nil {
				analysis.Analysis = _analysis
			}
		}
	}
}

func getMetadata(columns []*string, values []*string) (map[string]string, string) {
	metadata := make(map[string]string)
	id := ""
	for i, column := range columns {
		if values[i] != nil && *values[i] != "" && strings.Contains(strings.ToLower(*column), "id") {
			id = strings.Split(*values[i], " ")[0] // e.g. sg-123 (vpc-123	)
		} else if values[i] != nil && *values[i] != "" {
			metadata[*column] = *values[i]
		}
	}

	return metadata, id
}
