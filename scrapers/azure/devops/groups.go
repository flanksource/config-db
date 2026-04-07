package devops

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/lib/pq"
)

func (ado AzureDevopsScraper) scrapeGroups(
	ctx api.ScrapeContext,
	client *AzureDevopsClient,
	config v1.AzureDevops,
) v1.ScrapeResults {
	groups, err := client.GetGroups(ctx)
	if err != nil {
		var results v1.ScrapeResults
		results.Errorf(err, "failed to list groups for %s", config.Organization)
		return results
	}

	ctx.Logger.V(3).Infof("[%s] found %d groups", config.Organization, len(groups))

	var (
		externalGroups     []dutyModels.ExternalGroup
		externalUsers      []dutyModels.ExternalUser
		externalUserGroups []dutyModels.ExternalUserGroup
	)

	for _, group := range groups {
		groupID, err := DescriptorID(group.Descriptor)
		if err != nil {
			ctx.Logger.Errorf("failed to create group ID for %s: %v", group.DisplayName, err)
			continue
		}

		aliases := append(DescriptorAliases(group.Descriptor), group.PrincipalName)
		externalGroups = append(externalGroups, dutyModels.ExternalGroup{
			ID:        groupID,
			Name:      group.DisplayName,
			Aliases:   pq.StringArray(aliases),
			Tenant:    config.Organization,
			GroupType: "AzureDevOps",
		})

		members, err := client.GetGroupMembers(ctx, group.Descriptor)
		if err != nil {
			ctx.Logger.Warnf("failed to get members for group %s: %v", group.DisplayName, err)
			continue
		}

		if len(members) == 0 {
			continue
		}

		var memberDescriptors []string
		for _, m := range members {
			memberDescriptors = append(memberDescriptors, m.MemberDescriptor)
		}

		resolved, err := client.GetIdentitiesByDescriptor(ctx, memberDescriptors)
		if err != nil {
			ctx.Logger.Warnf("failed to resolve members for group %s: %v", group.DisplayName, err)
			continue
		}

		for _, identity := range resolved {
			if !identity.IsActive {
				continue
			}
			if identity.IsContainer {
				nestedGroupID, err := DescriptorID(identity.Descriptor)
				if err != nil {
					continue
				}
				nestedAliases := append(DescriptorAliases(identity.Descriptor), identity.SubjectDescriptor)
				nestedAliases = append(nestedAliases, DescriptorAliases(identity.SubjectDescriptor)...)
				externalGroups = append(externalGroups, dutyModels.ExternalGroup{
					ID:        nestedGroupID,
					Name:      identity.ProviderDisplayName,
					Aliases:   pq.StringArray(nestedAliases),
					Tenant:    config.Organization,
					GroupType: "AzureDevOps",
				})
			} else {
				name := ResolvedIdentityName(identity, "")
				email := ""
				if mail, ok := identity.Properties["Mail"]; ok {
					email = mail.Value
				}
				if email == "" && name == "" {
					continue
				}
				if email == "" {
					email = name
				}

				userID, err := DescriptorID(identity.Descriptor)
				if err != nil {
					continue
				}

				externalUsers = append(externalUsers, dutyModels.ExternalUser{
					ID:       userID,
					Name:     name,
					Email:    &email,
					Aliases:  pq.StringArray{email, identity.Descriptor, identity.SubjectDescriptor},
					Tenant:   config.Organization,
					UserType: "AzureDevOps",
				})

				externalUserGroups = append(externalUserGroups, dutyModels.ExternalUserGroup{
					ExternalUserID:  userID,
					ExternalGroupID: groupID,
				})
			}
		}
	}

	return v1.ScrapeResults{{
		BaseScraper:        config.BaseScraper,
		ExternalGroups:     externalGroups,
		ExternalUsers:      externalUsers,
		ExternalUserGroups: externalUserGroups,
	}}
}
