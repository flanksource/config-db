package aws

import (
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	"github.com/aws/aws-sdk-go-v2/service/configservice/types"
	v1 "github.com/flanksource/config-db/api/v1"
)

func (aws Scraper) config(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Compliance {
		return
	}

	rules, err := ctx.Config.DescribeConfigRules(ctx, nil)
	if err != nil {
		results.Errorf(err, "Failed to describe config rules")
		return
	}

	for _, rule := range rules.ConfigRules {
		details, err := ctx.Config.GetComplianceDetailsByConfigRule(ctx, &configservice.GetComplianceDetailsByConfigRuleInput{
			ConfigRuleName:  rule.ConfigRuleName,
			ComplianceTypes: []types.ComplianceType{types.ComplianceTypeNonCompliant},
		})
		if err != nil {
			results.Errorf(err, "Failed to describe config rules")
			return
		}
		for _, compliance := range details.EvaluationResults {
			if compliance.EvaluationResultIdentifier == nil {
				continue
			}
			if compliance.EvaluationResultIdentifier.EvaluationResultQualifier == nil {
				continue
			}
			obj := compliance.EvaluationResultIdentifier.EvaluationResultQualifier
			results.Analysis(*obj.ConfigRuleName, *obj.ResourceType, *obj.ResourceId).
				Message(deref(rule.Description)).
				Message(deref(compliance.Annotation))
		}
	}

}
