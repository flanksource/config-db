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
	"github.com/pkg/errors"
)

type AWSScraper struct {
}

func errorf(err error, msg string, args ...interface{}) []v1.ScrapeResult {
	logger.Errorf(msg+": "+err.Error(), args...)
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

func (aws AWSScraper) Scrape(ctx v1.ScrapeContext, config v1.ConfigScraper, _ v1.Manager) []v1.ScrapeResult {
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

		subnets, err := EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{})
		if err != nil {
			return errorf(err, "failed to describe subnets")
		}
		subnetZoneMapping := make(map[string]v1.ScrapeResult)
		for _, subnet := range subnets.Subnets {
			az := *subnet.AvailabilityZone
			result := v1.ScrapeResult{
				Type:    "Subnet",
				Id:      *subnet.SubnetId,
				Subnet:  *subnet.SubnetId,
				Config:  subnet,
				Account: *caller.Account,
				Network: *subnet.VpcId,
				Zone:    az,
				Region:  az[0 : len(az)-1],
			}
			subnetZoneMapping[*subnet.SubnetId] = result
			results = append(results, result)
		}
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
				patches, err := listPatches(SSM, ctx, instance.InstanceId, nil)
				if err != nil {
					return errorf(err, "failed to get patches for %s", instance.InstanceId)
				}
				instance.Patches = patches
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

				for _, detail := range details.EvaluationResults {
					instance.Compliance = append(instance.Compliance, NewComplianceDetail(detail))
				}
			}
			if awsConfig.TrustedAdvisorCheck {
				trustedAdvisorCheckResults, err := getTrustedAdvisorCheckResults(ctx, session)
				if err != nil {
					return errorf(err, "Failed to get trusted advisor check results")
				}
				for _, instance := range instances {
					trustedAdvisorChecks := []TrustedAdvisorCheck{}
					for _, checkResult := range trustedAdvisorCheckResults {
						check := checkResult.TrustedAdvisorCheckFromCheckResult(instance)
						if check != nil {
							trustedAdvisorChecks = append(trustedAdvisorChecks, *check)
						}
						instance.TrsutedAdvisorChecks = trustedAdvisorChecks
					}

				}
			}

			results = append(results, v1.ScrapeResult{
				Config:  instance,
				Type:    "EC2Instance",
				Network: instance.VpcId,
				Subnet:  instance.SubnetId,
				Zone:    subnetZoneMapping[instance.SubnetId].Zone,
				Region:  subnetZoneMapping[instance.SubnetId].Region,
				Name:    instance.GetHostname(),
				Account: *caller.Account,
				Id:      instance.InstanceId})
		}
	}
	return results
}

func listPatches(SSM *ssm.Client, ctx v1.ScrapeContext, instanceId string, token *string) ([]PatchDetail, error) {
	var list = []PatchDetail{}

	patches, err := SSM.DescribeInstancePatches(ctx, &ssm.DescribeInstancePatchesInput{
		InstanceId: &instanceId,
		MaxResults: 50,
		NextToken:  token,
	})

	if err != nil {
		return nil, errors.Wrapf(err, "failed to get patches for %s", instanceId)
	}

	for _, p := range patches.Patches {
		if p.State != "NotApplicable" {
			list = append(list, NewPatchDetail(p))
		}
	}
	if patches.NextToken != nil {
		nextList, err := listPatches(SSM, ctx, instanceId, patches.NextToken)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get patches for %s", instanceId)
		}
		list = append(list, nextList...)
	}
	return list, nil
}

func (t *TrustedAdvisorCheckResult) TrustedAdvisorCheckFromCheckResult(instance *Instance) *TrustedAdvisorCheck {
	for _, resource := range t.FlaggedResources {
		if resource.Metadata["Instance ID"] == instance.InstanceId {
			delete(resource.Metadata, "Instance ID")
			delete(resource.Metadata, "Region/AZ")
			delete(resource.Metadata, "Instance Name")
			delete(resource.Metadata, "Instance Type")

			savings := strings.TrimPrefix(resource.Metadata["Estimated Monthly Savings"], "$")
			if savings == "" {
				continue
			}
			estimatedMonthlySavingsUSD, err := strconv.ParseFloat(savings, 64)
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
		if strings.Contains(resource.Metadata["Volume Attachment"], instance.InstanceId) {
			delete(resource.Metadata, "Region")
			delete(resource.Metadata, "Volume Name")
			delete(resource.Metadata, "Volume ID")
			resource.Metadata["volume_attachment"] = strings.TrimSuffix(resource.Metadata["Volume Attachment"], ":"+instance.InstanceId)
			delete(resource.Metadata, "Volume Attachment")
			return &TrustedAdvisorCheck{
				Metdata:       resource.Metadata,
				CheckId:       t.CheckId,
				CheckName:     t.CheckName,
				CheckCategory: t.CheckCategory,
				CheckStatus:   t.Status,
			}
		}
		for key := range instance.SecurityGroups {
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
