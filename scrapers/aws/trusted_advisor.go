package aws

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/support"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

func mapCategoryToAnalysisType(category string) v1.AnalysisType {
	switch category {
	case "cost_optimizing", "cost":
		return v1.AnalysisTypeCost
	case "performance":
		return v1.AnalysisTypePerformance
	case "fault_tolerance":
		return v1.AnalysisTypeReliability
	case "recommendation":
		return v1.AnalysisTypeRecommendation
	default:
		return v1.AnalysisTypeOther
	}
}

func mapSeverity(severity string) v1.Severity {
	switch severity {
	case "Red":
		return v1.SeverityCritical
	case "Yellow":
		return v1.SeverityLow
	}

	return v1.SeverityInfo
}

func (aws Scraper) trustedAdvisor(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if config.Excludes("trusted_advisor") {
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
					configType = "AWS::Region"
					delete(metadata, "Region")
				} else {
					id = *ctx.Caller.Account
					configType = "AWS::::Account"
				}
			}
			analysis := results.Analysis(*check.Name, configType, id)
			analysis.AnalysisType = mapCategoryToAnalysisType(*check.Category)
			analysis.Severity = mapSeverity(metadata["Status"])
			delete(metadata, "Status")
			analysis.Message(deref(check.Description))
			analysis.Source = "AWS Trusted Advisor"

			if _analysis, err := utils.ToJSONMap(metadata); err != nil {
				analysis.Analysis = _analysis
			}

			logger.Infof("%s %s %s %v", *check.Name, configType, id, metadata)
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
