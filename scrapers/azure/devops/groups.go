package devops

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/lib/pq"
)

// uniqueAliases returns the input slice with empty strings and duplicates removed.
func uniqueAliases(items ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

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

	// Deduplicate locally by descriptor (the only stable identifier the ADO API
	// gives us). The emitted ExternalUser/ExternalGroup records intentionally
	// have no `ID` set: identities seen here are reconciled by alias overlap in
	// the SQL merge against the AAD scraper's authoritative records, and the
	// AAD-supplied ID wins.
	groupByDescriptor := map[string]dutyModels.ExternalGroup{}
	userByDescriptor := map[string]dutyModels.ExternalUser{}
	membershipSeen := map[string]bool{}
	var externalUserGroups []v1.ExternalUserGroup

	addGroup := func(descriptor string, g dutyModels.ExternalGroup) {
		if existing, ok := groupByDescriptor[descriptor]; ok {
			g.Aliases = pq.StringArray(uniqueAliases(append([]string(existing.Aliases), g.Aliases...)...))
			if g.Name == "" {
				g.Name = existing.Name
			}
		}
		groupByDescriptor[descriptor] = g
	}

	addUser := func(descriptor string, u dutyModels.ExternalUser) {
		if existing, ok := userByDescriptor[descriptor]; ok {
			u.Aliases = pq.StringArray(uniqueAliases(append([]string(existing.Aliases), u.Aliases...)...))
			if u.Name == "" {
				u.Name = existing.Name
			}
			if u.Email == nil || *u.Email == "" {
				u.Email = existing.Email
			}
		}
		userByDescriptor[descriptor] = u
	}

	addMembership := func(userAliases, groupAliases []string) {
		key := joinForKey(userAliases) + "||" + joinForKey(groupAliases)
		if membershipSeen[key] {
			return
		}
		membershipSeen[key] = true
		externalUserGroups = append(externalUserGroups, v1.ExternalUserGroup{
			ExternalUserAliases:  userAliases,
			ExternalGroupAliases: groupAliases,
		})
	}

	for _, group := range groups {
		groupAliases := uniqueAliases(append(DescriptorAliases(group.Descriptor), group.PrincipalName, group.Descriptor)...)
		addGroup(group.Descriptor, dutyModels.ExternalGroup{
			Name:      group.DisplayName,
			Aliases:   pq.StringArray(groupAliases),
			Tenant:    config.Organization,
			GroupType: "AzureDevOps",
		})

		members, err := client.GetGroupMembers(ctx, group.Descriptor)
		if err != nil {
			ctx.Logger.Warnf("failed to get members for group %s: %v", group.DisplayName, err)
			continue
		}

		ctx.Logger.V(3).Infof("[%s] group %q: %d members", config.Organization, group.DisplayName, len(members))
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
				// Nested group — record it. Group-in-group is represented by adding
				// the nested group's direct users to this parent group when that group
				// is processed in the outer loop.
				addGroup(identity.Descriptor, dutyModels.ExternalGroup{
					Name:      identity.ProviderDisplayName,
					Aliases:   pq.StringArray(identityAliases(identity, "")),
					Tenant:    config.Organization,
					GroupType: "AzureDevOps",
				})
				continue
			}

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

			userAliases := identityAliases(identity, email)
			addUser(identity.Descriptor, dutyModels.ExternalUser{
				Name:     name,
				Email:    &email,
				Aliases:  pq.StringArray(userAliases),
				Tenant:   config.Organization,
				UserType: "AzureDevOps",
			})

			addMembership(userAliases, groupAliases)
		}
	}

	externalGroups := make([]dutyModels.ExternalGroup, 0, len(groupByDescriptor))
	for _, g := range groupByDescriptor {
		externalGroups = append(externalGroups, g)
	}
	externalUsers := make([]dutyModels.ExternalUser, 0, len(userByDescriptor))
	for _, u := range userByDescriptor {
		externalUsers = append(externalUsers, u)
	}

	ctx.Logger.Infof("scrapeGroups[%s]: emitted %d groups, %d users, %d memberships from %d source groups",
		config.Organization, len(externalGroups), len(externalUsers), len(externalUserGroups), len(groups))

	return v1.ScrapeResults{{
		BaseScraper:        config.BaseScraper,
		ExternalGroups:     externalGroups,
		ExternalUsers:      externalUsers,
		ExternalUserGroups: externalUserGroups,
	}}
}

// joinForKey is a stable string join used as a map key for deduping membership
// entries by alias content. The separator '\x00' is impossible inside ADO
// descriptors and emails, so it cannot collide with alias content.
func joinForKey(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\x00"
		}
		out += p
	}
	return out
}
