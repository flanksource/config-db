package aws

// func scrapeTrustedAdvisor(ctx *v1.ScrapeContext, awsConfig v1.AWS) ([]*v1.ScrapeResult, error) {
// 	if awsConfig.TrustedAdvisorCheck {
// 		trustedAdvisorCheckResults, err := getTrustedAdvisorCheckResults(ctx, session)
// 		if err != nil {
// 			return results.Errorf(err, "Failed to get trusted advisor check results")
// 		}
// 		for _, instance := range instances {
// 			trustedAdvisorChecks := []TrustedAdvisorCheck{}
// 			for _, checkResult := range trustedAdvisorCheckResults {
// 				check := checkResult.TrustedAdvisorCheckFromCheckResult(instance)
// 				if check != nil {
// 					trustedAdvisorChecks = append(trustedAdvisorChecks, *check)
// 				}
// 				instance.TrsutedAdvisorChecks = trustedAdvisorChecks
// 			}

// 		}
// 		return nil, nil
// 	}
// }

// // TrustedAdvisorCheckFromCheckResult ...
// func (t *TrustedAdvisorCheckResult) TrustedAdvisorCheckFromCheckResult(instance *Instance) *TrustedAdvisorCheck {
// 	for _, resource := range t.FlaggedResources {
// 		if resource.Metadata["Instance ID"] == instance.InstanceID {
// 			delete(resource.Metadata, "Instance ID")
// 			delete(resource.Metadata, "Region/AZ")
// 			delete(resource.Metadata, "Instance Name")
// 			delete(resource.Metadata, "Instance Type")

// 			savings := strings.TrimPrefix(resource.Metadata["Estimated Monthly Savings"], "$")
// 			if savings == "" {
// 				continue
// 			}
// 			estimatedMonthlySavingsUSD, err := strconv.ParseFloat(savings, 64)
// 			if err != nil {
// 				logger.Errorf("error parsing estimated monthly savings: %s", err)
// 			}
// 			delete(resource.Metadata, "Estimated Monthly Savings")
// 			return &TrustedAdvisorCheck{
// 				Metdata:                 resource.Metadata,
// 				CheckID:                 t.CheckID,
// 				CheckName:               t.CheckName,
// 				CheckCategory:           t.CheckCategory,
// 				CheckStatus:             t.Status,
// 				EstimatedMonthlySavings: estimatedMonthlySavingsUSD,
// 			}
// 		}
// 		if strings.Contains(resource.Metadata["Volume Attachment"], instance.InstanceID) {
// 			delete(resource.Metadata, "Region")
// 			delete(resource.Metadata, "Volume Name")
// 			delete(resource.Metadata, "Volume ID")
// 			resource.Metadata["volume_attachment"] = strings.TrimSuffix(resource.Metadata["Volume Attachment"], ":"+instance.InstanceID)
// 			delete(resource.Metadata, "Volume Attachment")
// 			return &TrustedAdvisorCheck{
// 				Metdata:       resource.Metadata,
// 				CheckID:       t.CheckID,
// 				CheckName:     t.CheckName,
// 				CheckCategory: t.CheckCategory,
// 				CheckStatus:   t.Status,
// 			}
// 		}
// 		for key := range instance.SecurityGroups {
// 			if strings.Contains(resource.Metadata["Security Group ID"], key) {
// 				delete(resource.Metadata, "Region")
// 				return &TrustedAdvisorCheck{
// 					Metdata:       resource.Metadata,
// 					CheckID:       t.CheckID,
// 					CheckName:     t.CheckName,
// 					CheckCategory: t.CheckCategory,
// 					CheckStatus:   t.Status,
// 				}
// 			}
// 		}
// 	}
// 	return nil
// }

// func getTrustedAdvisorCheckResults(ctx *AWSContext) (results []*TrustedAdvisorCheckResult, err error) {

// 	trustAdvidorChecksDescribeInput := &support.DescribeTrustedAdvisorChecksInput{
// 		Language: strPtr("en"),
// 	}
// 	trustAdvidorChecksDescribeOutput, err := ctx.Support.DescribeTrustedAdvisorChecks(ctx, trustAdvidorChecksDescribeInput)
// 	if err != nil {
// 		return nil, err
// 	}
// 	for _, check := range trustAdvidorChecksDescribeOutput.Checks {
// 		// Support.DescribeTrustedAdvisorCheckResult()
// 		trustedAdvisorCheckResultInput := &support.DescribeTrustedAdvisorCheckResultInput{
// 			Language: strPtr("en"),
// 			CheckId:  check.Id,
// 		}
// 		trustedAdvisorCheckResultOutput, err := Support.DescribeTrustedAdvisorCheckResult(ctx, trustedAdvisorCheckResultInput)
// 		if err != nil {
// 			return nil, err
// 		}
// 		//Passing check.Metadata as it desrcibes the order of the heading in the Check Result field.
// 		trustAdvisorCheckResult := NewTrustedAdvisorCheckResult(trustedAdvisorCheckResultOutput.Result, *check.Name, *check.Description, *check.Category, check.Metadata)

// 		results = append(results, trustAdvisorCheckResult)
// 	}
// 	return results, nil
// }
