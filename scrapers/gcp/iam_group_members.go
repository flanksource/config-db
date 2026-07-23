package gcp

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/lib/pq"
	"github.com/samber/lo"
	cloudidentity "google.golang.org/api/cloudidentity/v1"
)

// leafMember is a resolved non-group principal discovered while expanding a group.
type leafMember struct {
	email    string
	userType string
}

// FetchGroupMemberships expands each bound Google group into its transitive
// user / service-account members via the Cloud Identity API, emitting the
// external_user_groups edges that let group grants unwrap to individual users.
//
// The config_access_unwrapped view joins external_user_groups a single level,
// so nested groups are flattened: every transitive leaf member is linked
// directly to the bound group it was reached from.
func (Scraper) FetchGroupMemberships(ctx *GCPContext, config v1.GCP, groupEmails []string) (v1.ScrapeResults, error) {
	if len(groupEmails) == 0 {
		return nil, nil
	}

	svc, err := cloudidentity.NewService(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("error creating cloud identity client: %w", err)
	}

	exp := &groupExpander{ctx: ctx, svc: svc, memberships: map[string][]*cloudidentity.Membership{}}
	acc := newMembershipAccumulator(config.Project)

	var results v1.ScrapeResults
	for _, groupEmail := range groupEmails {
		leaves, nested, err := traverseGroup(groupEmail, exp.directMemberships, ctx.Warnf)
		if err != nil {
			results.Errorf(err, "failed to expand group %s", groupEmail)
			continue
		}
		acc.add(groupEmail, leaves, nested)
	}

	results = append(results, v1.ScrapeResult{
		BaseScraper:        config.BaseScraper,
		ExternalUsers:      acc.users,
		ExternalGroups:     acc.groups,
		ExternalUserGroups: acc.userGroups,
	})

	return results, nil
}

// traverseGroup walks group membership breadth-first from rootEmail, returning
// the transitive leaf members and the nested group emails encountered. fetch
// yields a group's direct memberships; a fetch failure below the root is passed
// to warn and that subtree is skipped. A visited set makes cycles safe. Pure and
// unit-tested via an in-memory fetcher.
func traverseGroup(
	rootEmail string,
	fetch func(string) ([]*cloudidentity.Membership, error),
	warn func(format string, args ...any),
) ([]leafMember, []string, error) {
	rootMembers, err := fetch(rootEmail)
	if err != nil {
		return nil, nil, err
	}

	var leaves []leafMember
	var nested []string
	visited := map[string]struct{}{rootEmail: {}}
	queue := [][]*cloudidentity.Membership{rootMembers}

	for len(queue) > 0 {
		batch := queue[0]
		queue = queue[1:]

		for _, m := range batch {
			if m.PreferredMemberKey == nil || m.PreferredMemberKey.Id == "" {
				continue
			}
			email := m.PreferredMemberKey.Id

			switch m.Type {
			case "GROUP":
				nested = append(nested, email)
				if contains(visited, email) {
					continue
				}
				visited[email] = struct{}{}
				sub, err := fetch(email)
				if err != nil {
					warn("gcp iam: failed to expand nested group %s: %v", email, err)
					continue
				}
				queue = append(queue, sub)
			case "USER":
				leaves = append(leaves, leafMember{email: email, userType: "User"})
			case "SERVICE_ACCOUNT":
				leaves = append(leaves, leafMember{email: email, userType: "ServiceAccount"})
			}
		}
	}

	return leaves, nested, nil
}

// membershipAccumulator deduplicates the users, groups, and user→group edges
// discovered across all expanded bound groups.
type membershipAccumulator struct {
	project    string
	userGroups []v1.ExternalUserGroup
	users      []models.ExternalUser
	groups     []models.ExternalGroup
	seenEdge   map[string]struct{}
	seenUser   map[string]struct{}
	seenGroup  map[string]struct{}
}

func newMembershipAccumulator(project string) *membershipAccumulator {
	return &membershipAccumulator{
		project:   project,
		seenEdge:  make(map[string]struct{}),
		seenUser:  make(map[string]struct{}),
		seenGroup: make(map[string]struct{}),
	}
}

func (a *membershipAccumulator) add(groupEmail string, leaves []leafMember, nested []string) {
	for _, leaf := range leaves {
		if edgeKey := leaf.email + "\x00" + groupEmail; !contains(a.seenEdge, edgeKey) {
			a.seenEdge[edgeKey] = struct{}{}
			a.userGroups = append(a.userGroups, v1.ExternalUserGroup{
				ExternalUserAliases:  []string{leaf.email},
				ExternalGroupAliases: []string{groupEmail},
			})
		}

		if !contains(a.seenUser, leaf.email) {
			a.seenUser[leaf.email] = struct{}{}
			a.users = append(a.users, models.ExternalUser{
				Aliases:  pq.StringArray{leaf.email},
				Name:     leaf.email,
				Tenant:   a.project,
				UserType: leaf.userType,
				Email:    lo.ToPtr(leaf.email),
			})
		}
	}

	for _, nestedEmail := range nested {
		if !contains(a.seenGroup, nestedEmail) {
			a.seenGroup[nestedEmail] = struct{}{}
			a.groups = append(a.groups, models.ExternalGroup{
				Aliases:   pq.StringArray{nestedEmail},
				Name:      nestedEmail,
				Tenant:    a.project,
				GroupType: "Group",
			})
		}
	}
}

// groupExpander fetches Google-group memberships from Cloud Identity, memoizing
// each group's direct memberships so shared subgroups are fetched once.
type groupExpander struct {
	ctx         *GCPContext
	svc         *cloudidentity.Service
	memberships map[string][]*cloudidentity.Membership
}

func (e *groupExpander) directMemberships(groupEmail string) ([]*cloudidentity.Membership, error) {
	if cached, ok := e.memberships[groupEmail]; ok {
		return cached, nil
	}

	lookup, err := e.svc.Groups.Lookup().GroupKeyId(groupEmail).Context(e.ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("error looking up group %s: %w", groupEmail, err)
	}

	var members []*cloudidentity.Membership
	pageToken := ""
	for {
		call := e.svc.Groups.Memberships.List(lookup.Name).PageSize(500).Context(e.ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("error listing memberships of %s: %w", groupEmail, err)
		}
		members = append(members, resp.Memberships...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	e.memberships[groupEmail] = members
	return members, nil
}
