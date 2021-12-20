package aws

import (
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/support"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
)

type AWSScraper struct {
}

func errorf(err error, msg string, args ...interface{}) []v1.ScrapeResult {
	logger.Errorf(err.Error()+msg, args...)
	return nil
}

func failf(msg string, args ...interface{}) []v1.ScrapeResult {
	logger.Errorf(msg, args...)
	return nil
}

func getKeys(instances map[string]*Instance) []string {
	ids := []string{}
	for id := range instances {
		ids = append(ids, id)
	}
	return ids
}
func strPtr(s string) *string {
	return &s
}

func (aws AWSScraper) Scrape(ctx v1.ScrapeContext, config v1.ConfigScraper) []v1.ScrapeResult {
	results := []v1.ScrapeResult{}

	for _, awsConfig := range config.AWS {

		session, err := NewSession(&ctx, *awsConfig.AWSConnection)
		if err != nil {
			return errorf(err, "failed to create AWS session")
		}

		STS := sts.NewFromConfig(*session)
		caller, err := STS.GetCallerIdentity(ctx, nil)
		if err != nil {
			return errorf(err, "failed to get identity")
		}
		logger.Infof("Scraping AWS account=%s user=%s", *caller.Account, *caller.UserId)

		EC2 := ec2.NewFromConfig(*session)
		SSM := ssm.NewFromConfig(*session)

		describeInput := &ec2.DescribeInstancesInput{}

		describeOutput, err := EC2.DescribeInstances(ctx, describeInput)
		if err != nil {
			return errorf(err, "failed to describe instances")
		}

		instances := make(map[string]*Instance)
		for r := range describeOutput.Reservations {
			for _, i := range describeOutput.Reservations[r].Instances {
				instances[*i.InstanceId] = NewInstance(i)
			}
		}

		if awsConfig.Inventory {
			inventory, err := SSM.GetInventory(ctx, nil)
			if err != nil {
				return errorf(err, "failed to get inventory")
			}

			for _, inv := range inventory.Entities {
				if data, ok := inv.Data["AWS:InstanceInformation"]; ok {
					instance, found := instances[*inv.Id]
					if !found {
						logger.Warnf("Inventory found for unknown instance %s", *inv.Id)
						continue
					}
					instance.Inventory = makeMap(instance.Inventory)
					for _, d := range data.Content {
						for k, v := range d {
							instance.Inventory[k] = v
						}
					}
					instance.Inventory["CaptureTime"] = *data.CaptureTime
				}
			}
		}

		if awsConfig.PatchStates {
			patchStates, err := SSM.DescribeInstancePatchStates(ctx, &ssm.DescribeInstancePatchStatesInput{
				InstanceIds: getKeys(instances),
			})
			if err != nil {
				return errorf(err, "failed to retrieeve patch states")
			}
			for _, patch := range patchStates.InstancePatchStates {
				instance, found := instances[*patch.InstanceId]
				if !found {
					logger.Warnf("Patch found for unknown instance %s", *patch.InstanceId)
					continue
				}
				if instance.PatchState != nil {
					logger.Warnf("Duplicate patch found for %s, %v and %v", instance.InstanceId, instance.PatchState.OperationEndTime, patch.OperationEndTime)
				}
				instance.PatchState = &patch
			}
		}
		for _, instance := range instances {
			if awsConfig.PatchDetails {

				patches, err := SSM.DescribeInstancePatches(ctx, &ssm.DescribeInstancePatchesInput{
					InstanceId: &instance.InstanceId,
				})
				if err != nil {
					return errorf(err, "failed to get patches for %s", instance.InstanceId)
				}

				for _, p := range patches.Patches {
					instance.Patches = append(instance.Patches, NewPatchDetail(p))
				}
			}

			if awsConfig.Compliance {
				Config := configservice.NewFromConfig(*session)
				details, err := Config.GetComplianceDetailsByResource(ctx, &configservice.GetComplianceDetailsByResourceInput{
					ResourceId:   &instance.InstanceId,
					ResourceType: strPtr("AWS::EC2::Instance"),
				})
				if err != nil {
					return errorf(err, "cannot get compliance details")
				}
				instance.Compliance = make(map[string]ComplianceDetail)

				for _, detail := range details.EvaluationResults {
					result := NewComplianceDetail(detail)
					instance.Compliance[result.Id] = result
				}
			}
		}
		trustedAdvisorCheckResults, err := getTrustedAdvisorCheckResults(ctx, session)
		if err != nil {
			logger.Errorf("Failed to get trusted advisor check results: %s", err)
		}
		for _, instance := range instances {
			trustedAdvisorChecks := []TrustedAdvisorCheck{}
			for _, checkResult := range trustedAdvisorCheckResults {
				check := checkResult.TrustedAdvisorCheckFromCheckResult(instance.InstanceId, instance.SecurityGroups)
				if check != nil {
					trustedAdvisorChecks = append(trustedAdvisorChecks, *check)
				}
				instance.TrsutedAdvisorChecks = trustedAdvisorChecks
			}
			results = append(results, v1.ScrapeResult{Config: instance, Type: "EC2Instance", Id: instance.InstanceId})

		}
	}
	return results
}

func (t *TrustedAdvisorCheckResult) TrustedAdvisorCheckFromCheckResult(instanceId string, securityGroups map[string]string) *TrustedAdvisorCheck {
	for _, resource := range t.FlaggedResources {
		if resource.Metadata["Instance ID"] == instanceId {
			delete(resource.Metadata, "Instance ID")
			delete(resource.Metadata, "Region/AZ")
			delete(resource.Metadata, "Instance Name")
			delete(resource.Metadata, "Instance Type")

			estimatedMonthlySavingsUSD, err := strconv.ParseFloat(strings.TrimPrefix(resource.Metadata["Estimated Monthly Savings"], "$"), 64)
			if err != nil {
				logger.Errorf("error parsing estimated monthly savings: %s", err)
			}
			delete(resource.Metadata, "Estimated Monthly Savings")
			return &TrustedAdvisorCheck{
				Metdata:                 resource.Metadata,
				CheckId:                 t.CheckId,
				CheckName:               t.CheckName,
				CheckCategory:           t.CheckCategory,
				CheckStatus:             t.Status,
				EstimatedMonthlySavings: estimatedMonthlySavingsUSD,
			}
		}
		if strings.Contains(resource.Metadata["Volume Attachment"], instanceId) {
			delete(resource.Metadata, "Region")
			delete(resource.Metadata, "Volume Name")
			delete(resource.Metadata, "Volume ID")
			resource.Metadata["volume_attachment"] = strings.TrimSuffix(resource.Metadata["Volume Attachment"], ":"+instanceId)
			delete(resource.Metadata, "Volume Attachment")
			return &TrustedAdvisorCheck{
				Metdata:       resource.Metadata,
				CheckId:       t.CheckId,
				CheckName:     t.CheckName,
				CheckCategory: t.CheckCategory,
				CheckStatus:   t.Status,
			}
		}
		for key := range securityGroups {
			if strings.Contains(resource.Metadata["Security Group ID"], key) {
				delete(resource.Metadata, "Region")
				return &TrustedAdvisorCheck{
					Metdata:       resource.Metadata,
					CheckId:       t.CheckId,
					CheckName:     t.CheckName,
					CheckCategory: t.CheckCategory,
					CheckStatus:   t.Status,
				}
			}
		}
	}
	logger.Tracef("No mappings found for the present resources with the checkID: %v", t.CheckId)
	return nil
}

func getTrustedAdvisorCheckResults(ctx v1.ScrapeContext, session *aws.Config) (results []*TrustedAdvisorCheckResult, err error) {
	session.Region = "us-east-1"
	Support := support.NewFromConfig(*session)
	trustAdvidorChecksDescribeInput := &support.DescribeTrustedAdvisorChecksInput{
		Language: strPtr("en"),
	}
	trustAdvidorChecksDescribeOutput, err := Support.DescribeTrustedAdvisorChecks(ctx, trustAdvidorChecksDescribeInput)
	if err != nil {
		return nil, err
	}
	for _, check := range trustAdvidorChecksDescribeOutput.Checks {
		// Support.DescribeTrustedAdvisorCheckResult()
		trustedAdvisorCheckResultInput := &support.DescribeTrustedAdvisorCheckResultInput{
			Language: strPtr("en"),
			CheckId:  check.Id,
		}
		trustedAdvisorCheckResultOutput, err := Support.DescribeTrustedAdvisorCheckResult(ctx, trustedAdvisorCheckResultInput)
		if err != nil {
			return nil, err
		}
		//Passing check.Metadata as it desrcibes the order of the heading in the Check Result field.
		trustAdvisorCheckResult := NewTrustedAdvisorCheckResult(trustedAdvisorCheckResultOutput.Result, *check.Name, *check.Description, *check.Category, check.Metadata)

		results = append(results, trustAdvisorCheckResult)
	}
	return results, nil
}
