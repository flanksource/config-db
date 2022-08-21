package aws

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/support"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
)

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
			externalType := getExternalTypeById(id)
			if externalType == "" {
				if metadata["Bucket Name"] != "" {
					id = metadata["Bucket Name"]
					delete(metadata, "Bucket Name")
					externalType = "AWS::S3::Bucket"
				} else if metadata["IAM User"] != "" {
					id = metadata["IAM User"]
					delete(metadata, "IAM User")
					externalType = "AWS::IAM::User"
				} else if metadata["Hosted Zone Name"] != "" {
					id = metadata["Hosted Zone Name"]
					externalType = "AWS::Route53::HostedZone"
					delete(metadata, "Hosted Zone Name")
				} else if metadata["User Name (IAM or Root)"] != "" {
					id = metadata["User Name (IAM or Root)"]
					externalType = "AWS::IAM::User"
					delete(metadata, "User Name (IAM or Root)")
				} else if metadata["Region"] != "" {
					id = metadata["Region"]
					externalType = "AWS::Region"
					delete(metadata, "Region")
				} else {
					id = *ctx.Caller.Account
					externalType = "AWS::::Account"
				}
			}
			analysis := results.Analysis(*check.Name, externalType, id)
			analysis.AnalysisType = *check.Category
			analysis.Message(deref(check.Description))
			analysis.Analysis = metadata

			logger.Infof("%s %s %s %v", *check.Name, externalType, id, metadata)
		}
	}
}

func getMetadata(columns []string, values []string) (map[string]string, string) {
	metadata := make(map[string]string)
	id := ""
	for i, column := range columns {
		if values[i] != "" && strings.Contains(strings.ToLower(column), "id") {
			id = strings.Split(values[i], " ")[0] // e.g. sg-123 (vpc-123	)
		} else if values[i] != "" {
			metadata[column] = values[i]
		}
	}

	return metadata, id
}
