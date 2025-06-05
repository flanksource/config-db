package gcp

import (
	"fmt"
	"strings"
	"time"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	uuidV5 "github.com/gofrs/uuid/v5"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

type GCPContext struct {
	api.ScrapeContext
	ClientOpts []option.ClientOption
}

type Scraper struct {
}

func NewGCPContext(ctx api.ScrapeContext, gcpConfig v1.GCP) (*GCPContext, error) {
	var opts []option.ClientOption
	if gcpConfig.ConnectionName != "" {
		if err := gcpConfig.GCPConnection.HydrateConnection(ctx); err != nil {
			return nil, fmt.Errorf("%w", err)
		}
		c, err := google.CredentialsFromJSON(ctx, []byte(gcpConfig.GCPConnection.Credentials.ValueStatic))
		if err != nil {
			return nil, fmt.Errorf("%w", err)
		}
		opts = append(opts, option.WithCredentials(c))
	}

	return &GCPContext{
		ScrapeContext: ctx,
		ClientOpts:    opts,
	}, nil
}

func parseGCPConfigClass(assetType string) string {
	parts := strings.Split(assetType, ".googleapis.com/")
	if len(parts) != 2 {
		return "GCP::" + assetType
	}

	// compute.googleapis.com/InstanceSettings => Compute::InstanceSettings
	return fmt.Sprintf("%s::%s", lo.PascalCase(parts[0]), lo.PascalCase(parts[1]))
}

type ResourceData struct {
	ID        string
	Name      string
	CreatedAt time.Time
	Region    string
	Zone      string
	Labels    map[string]string
	URL       string
}

func getRegionFromZone(zone string) string {
	parts := strings.Split(zone, "-")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[:2], "-")
}

func parseResourceData(data *structpb.Struct) ResourceData {
	labels := make(map[string]string)
	if labelsField, exists := data.Fields["labels"]; exists {
		if labelsStruct := labelsField.GetStructValue(); labelsStruct != nil {
			for key, value := range labelsStruct.Fields {
				if strValue := value.GetStringValue(); strValue != "" {
					labels[key] = strValue
				}
			}
		}
	}

	createdAtRaw := data.Fields["creationTimestamp"].GetStringValue()
	createdAt, _ := time.Parse("2006-01-02T15:04:05.000-07:00", createdAtRaw)

	zone := data.Fields["location"].GetStringValue()
	region := getRegionFromZone(zone)

	return ResourceData{
		ID:        data.Fields["id"].GetStringValue(),
		Name:      data.Fields["name"].GetStringValue(),
		CreatedAt: createdAt,
		Labels:    labels,
		Zone:      data.Fields["location"].GetStringValue(),
		URL:       data.Fields["selfLink"].GetStringValue(),
		Region:    region,
	}
}

func getLink(rd ResourceData) *types.Property {
	return &types.Property{
		Name: "URL",
		// TODO: Add GCP Icons
		//Icon: resourceType,
		Links: []types.Link{
			{
				Text: types.Text{Label: "Console"},
				URL:  rd.URL,
			},
		},
	}
}

var defaultIgnoreList = []string{
	"compute.googleapis.com/InstanceSettings",
	"serviceusage.googleapis.com/Service",
}

func generateConsistentID(input string) uuid.UUID {
	gen := uuidV5.NewV5(uuidV5.NamespaceOID, input)
	return uuid.UUID(gen)
}

func parseGCPMember(member string) (userType, name, email string, found bool) {
	if strings.HasPrefix(member, "user:") {
		email = strings.TrimPrefix(member, "user:")
		return "User", email, email, true
	} else if strings.HasPrefix(member, "serviceAccount:") {
		email = strings.TrimPrefix(member, "serviceAccount:")
		return "ServiceAccount", email, email, true
	} else if strings.HasPrefix(member, "group:") {
		name = strings.TrimPrefix(member, "group:")
		return "Group", name, name, true
	}

	return "", member, "", false
}

func (gcp Scraper) FetchAllAssets(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults

	req := &assetpb.ListAssetsRequest{
		Parent:      fmt.Sprintf("projects/%s", config.Project),
		ContentType: assetpb.ContentType_RESOURCE,
		AssetTypes:  []string{".*.googleapis.com.*"},
	}

	if len(config.Include) > 0 {
		req.AssetTypes = config.Include
	}

	assetClient, err := asset.NewClient(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("error creating asset client: %w", err)
	}
	defer func() {
		if err := assetClient.Close(); err != nil {
			ctx.Warnf("gcp assets: failed to close asset client: %v", err)
		}
	}()

	baseTags := []v1.Tag{{Name: "project", Value: config.Project}}
	ignoreList := append(defaultIgnoreList, config.Exclude...)

	it := assetClient.ListAssets(ctx, req)
	for {
		asset, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing assets: %w", err)
		}

		if lo.Contains(ignoreList, asset.AssetType) {
			continue
		}

		rd := parseResourceData(asset.Resource.Data)

		configClass := parseGCPConfigClass(asset.AssetType)
		configType := fmt.Sprintf("GCP::%s", configClass)

		tags := baseTags
		if rd.Region != "" {
			tags = append(tags,
				v1.Tag{Name: "region", Value: rd.Region},
				v1.Tag{Name: "zone", Value: rd.Zone},
			)
		}

		res := v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			ID:          lo.CoalesceOrEmpty(rd.ID, rd.Name),
			Name:        rd.Name,
			Config:      asset.Resource.Data,
			ConfigClass: configClass,
			Type:        configType,
			CreatedAt:   lo.ToPtr(rd.CreatedAt),
			Labels:      rd.Labels,
			Tags:        tags,
			Aliases:     []string{rd.Name, asset.Name},
			Properties:  []*types.Property{getLink(rd)},
		}

		if rd.ID != "" {
			res.Aliases = append(res.Aliases, rd.ID)
		}

		results = append(results, res)
	}

	return results, nil
}

// FetchIAMPolicies scrapes external users and roles.
func (gcp Scraper) FetchIAMPolicies(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults

	req := &assetpb.ListAssetsRequest{
		Parent:      fmt.Sprintf("projects/%s", config.Project),
		ContentType: assetpb.ContentType_IAM_POLICY,
	}

	assetClient, err := asset.NewClient(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("error creating asset client for IAM policies: %w", err)
	}
	defer func() {
		if err := assetClient.Close(); err != nil {
			ctx.Warnf("gcp iam policies: failed to close asset client: %v", err)
		}
	}()

	// Track unique roles and users to avoid duplicates
	uniqueRoles := make(map[uuid.UUID]models.ExternalRole)
	uniqueUsers := make(map[uuid.UUID]models.ExternalUser)
	var configAccesses []v1.ExternalConfigAccess

	it := assetClient.ListAssets(ctx, req)
	for {
		asset, err := it.Next()
		if err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("error listing IAM policies: %w", err)
		}

		if asset.IamPolicy == nil {
			continue
		}

		// Extract resource ID from asset name (e.g., "//compute.googleapis.com/projects/project-id/zones/us-central1-a/instances/instance-name")
		resourceID := asset.Name
		if resourceID == "" {
			continue
		}

		for _, binding := range asset.IamPolicy.Bindings {
			// bind.Role could be
			// global: roles/cloudasset.owner
			// custom: projects/aditya-461913/roles/mycustomroleaditya (project scoped)
			roleID := generateConsistentID(binding.Role)
			if _, exists := uniqueRoles[roleID]; !exists {
				role := models.ExternalRole{
					ID:        roleID,
					Name:      binding.Role,
					AccountID: config.Project,
					ScraperID: ctx.ScrapeConfig().GetPersistedID(),
					RoleType:  lo.Ternary(strings.HasPrefix(binding.Role, "roles/"), "Global", "Custom"),
				}

				if strings.HasPrefix(binding.Role, "roles/") {
					role.RoleType = "Global"
				} else {
					role.RoleType = "Custom"

					// FIXME: Only custom roles should be tied to an account and scraper
				}

				uniqueRoles[roleID] = role
			}

			for _, member := range binding.Members {
				userType, name, email, found := parseGCPMember(member)
				if !found {
					continue
				}

				userID := generateConsistentID(email)
				if _, exists := uniqueUsers[userID]; !exists {
					externalUser := models.ExternalUser{
						ID:        userID,
						Name:      name,
						ScraperID: lo.FromPtr(ctx.ScrapeConfig().GetPersistedID()),
						AccountID: config.Project,
						CreatedAt: time.Now(), // We don't have this information
						UserType:  userType,
					}
					if email != "" {
						externalUser.Email = &email
					}
					uniqueUsers[userID] = externalUser
				}
			}
		}
	}

	var externalRoles []models.ExternalRole
	for _, role := range uniqueRoles {
		externalRoles = append(externalRoles, role)
	}

	var externalUsers []models.ExternalUser
	for _, user := range uniqueUsers {
		externalUsers = append(externalUsers, user)
	}

	results = append(results, v1.ScrapeResult{
		BaseScraper:   config.BaseScraper,
		ExternalRoles: externalRoles,
		ExternalUsers: externalUsers,
		ConfigAccess:  configAccesses,
	})

	return results, nil
}

func (Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.GCP) > 0
}

func (gcp Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	allResults := v1.ScrapeResults{}

	for _, gcpConfig := range ctx.ScrapeConfig().Spec.GCP {
		results := v1.ScrapeResults{}

		gcpCtx, err := NewGCPContext(ctx, gcpConfig)
		if err != nil {
			results.Errorf(err, "failed to create GCP context")
			allResults = append(allResults, results...)
			continue
		}

		assetResults, err := gcp.FetchAllAssets(gcpCtx, gcpConfig)
		if err != nil {
			results.Errorf(err, "failed to fetch GCP assets")
			allResults = append(allResults, results...)
			continue
		} else {
			allResults = append(allResults, assetResults...)
		}

		iamPolicyResults, err := gcp.FetchIAMPolicies(gcpCtx, gcpConfig)
		if err != nil {
			results.Errorf(err, "failed to fetch GCP IAM policies for project %s", gcpConfig.Project)
			allResults = append(allResults, results...)
		} else {
			allResults = append(allResults, iamPolicyResults...)
		}

		accessLogResults, err := gcp.FetchAuditLogs(gcpCtx, gcpConfig)
		if err != nil {
			results.Errorf(err, "failed to fetch GCP access logs for project %s", gcpConfig.Project)
			allResults = append(allResults, results...)
		} else {
			allResults = append(allResults, accessLogResults...)
		}
	}

	return allResults
}
