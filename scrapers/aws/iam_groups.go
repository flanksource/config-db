package aws

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/types"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/lib/pq"
	"github.com/samber/lo"
)

func awsIAMGroupAlias(accountID, groupName string) string {
	return fmt.Sprintf("aws://iam-group/%s/%s", accountID, groupName)
}

// iamGroups scrapes IAM Groups plus their user memberships.
// Gated on Includes("Groups"). Emits:
//   - AWS::IAM::Group ScrapeResult per group
//   - ExternalGroup per group (alias = group ARN, resolved at SaveResults)
//   - ExternalUserGroup per (user, group) pair
func (aws Scraper) iamGroups(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Groups") {
		return
	}
	ctx.Logger.V(2).Infof("scraping IAM groups")

	listOut, err := ctx.IAM.ListGroups(ctx, &iam.ListGroupsInput{})
	if err != nil {
		results.Errorf(err, "failed to list IAM groups")
		return
	}

	accountID := lo.FromPtr(ctx.Caller.Account)

	for _, group := range listOut.Groups {
		groupARN := lo.FromPtr(group.Arn)
		groupName := lo.FromPtr(group.GroupName)

		if config.ShouldExclude(v1.AWSIAMGroup, groupName, nil) {
			continue
		}

		getOut, err := ctx.IAM.GetGroup(ctx, &iam.GetGroupInput{GroupName: group.GroupName})
		if err != nil {
			results.Errorf(err, "failed to get IAM group %s", groupName)
			continue
		}

		groupMap, err := utils.ToJSONMap(group)
		if err != nil {
			results.Errorf(err, "failed to convert group to json")
			continue
		}

		sr := v1.ScrapeResult{
			Type:        v1.AWSIAMGroup,
			CreatedAt:   group.CreateDate,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMGroup, groupName, nil)},
			Config:      groupMap,
			ConfigClass: "Group",
			Name:        groupName,
			Aliases: []string{
				lo.FromPtr(group.GroupId),
				groupARN,
				awsIAMGroupAlias(accountID, groupName),
			},
			ID:      groupARN,
			Parents: []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: accountID}},
		}

		sr.ExternalGroups = append(sr.ExternalGroups, dutymodels.ExternalGroup{
			Aliases:   pq.StringArray{groupARN},
			Name:      groupName,
			Tenant:    accountID,
			GroupType: "IAM",
		})

		for _, user := range getOut.Users {
			userARN := lo.FromPtr(user.Arn)
			if userARN == "" {
				continue
			}
			sr.ExternalUserGroups = append(sr.ExternalUserGroups, v1.ExternalUserGroup{
				ExternalUserAliases:  []string{userARN},
				ExternalGroupAliases: []string{groupARN},
			})
		}

		*results = append(*results, sr)
	}
}
