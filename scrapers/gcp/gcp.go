package gcp

import (
	"fmt"
	"path"
	"strings"
	"time"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	"github.com/Jeffail/gabs/v2"
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
	var creds string
	if gcpConfig.ConnectionName != "" {
		if err := gcpConfig.GCPConnection.HydrateConnection(ctx); err != nil {
			return nil, fmt.Errorf("error hydrating gcp connection: %w", err)
		}
		creds = gcpConfig.GCPConnection.Credentials.ValueStatic
	}

	if gcpConfig.GCPConnection.Credentials != nil {
		var err error
		creds, err = ctx.GetEnvValueFromCache(*gcpConfig.GCPConnection.Credentials, ctx.Namespace())
		if err != nil {
			return nil, fmt.Errorf("error fetching credentials from k8s: %w", err)
		}
	}

	if creds != "" {
		c, err := google.CredentialsFromJSON(ctx, []byte(creds), "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, fmt.Errorf("error getting credentials from json: %w", err)
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
	Aliases   []string
	Raw       *structpb.Struct
}

func getRegionFromZone(zone string) string {
	parts := strings.Split(zone, "-")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[:2], "-")
}

func getFieldValue(data *structpb.Struct, keys []string) string {
	for _, field := range keys {
		if value, ok := data.Fields[field]; ok {
			if strValue := value.GetStringValue(); strValue != "" {
				return strValue
			}
		}
	}

	return ""
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

	createdAtRaw := getFieldValue(data, []string{"creationTimestamp", "createTime"})
	createdAt, _ := time.Parse(time.RFC3339, createdAtRaw)

	zone := getFieldValue(data, []string{"location", "gceZone"})
	if zone == "" {
		zone = getFieldValue(data, []string{"zone"})
		// For fields that may contain a full path, extract just the base name
		// e.g. https://www.googleapis.com/compute/v1/projects/<project-name>/zones/europe-west1-c
		if strings.Contains(zone, "/zones/") {
			zone = path.Base(zone)
		}
	}

	region := getRegionFromZone(zone)
	if region == "" {
		if r, ok := data.Fields["region"]; ok {
			region = path.Base(r.GetStringValue())
		}
	}

	if data.Fields["kind"].GetStringValue() == "storage#bucket" {
		if locationType := getFieldValue(data, []string{"locationType"}); locationType != "" {
			region = getFieldValue(data, []string{"location"})
			zone = ""
		}
	}

	id := data.Fields["id"].GetStringValue()
	name := data.Fields["name"].GetStringValue()
	selfLink := data.Fields["selfLink"].GetStringValue()

	return ResourceData{
		ID:        id,
		Name:      name,
		CreatedAt: createdAt,
		Labels:    labels,
		URL:       selfLink,
		Zone:      strings.ToLower(zone),
		Region:    strings.ToLower(region),
		Aliases:   []string{selfLink, name},
		Raw:       data,
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

		region := rd.Region
		if region != "" {
			tags = append(tags, v1.Tag{Name: "region", Value: region})
		}

		if rd.Zone != "" {
			tags = append(tags, v1.Tag{Name: "zone", Value: rd.Zone})
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
			Aliases:     append(rd.Aliases, asset.Name),
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

		if backupResults, err := gcp.scrapeCloudSQLBackupsForAllInstances(gcpCtx, gcpConfig, results); err != nil {
			results.Errorf(err, "failed to scrape Cloud SQL backups")
		} else {
			results = append(results, backupResults...)
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

func RelationshipResolver(assetType string, rd ResourceData) []v1.RelationshipResult {
	switch assetType {
	case v1.GCPInstance:
		return resolveGCPInstanceRelationships(rd)
	case v1.GCPSubnet:
		return resolveGCPSubnetRelationships(rd)
	case v1.GKECluster:
		return resolveGCPGKEClusterRelationships(rd)
	}
	return nil
}

func resolveGCPInstanceRelationships(rd ResourceData) (r []v1.RelationshipResult) {
	data := rd.Raw
	b, _ := data.MarshalJSON()
	p, _ := gabs.ParseJSON(b)
	selfExternalID := v1.ExternalID{ExternalID: data.Fields["selfLink"].GetStringValue(), ConfigType: v1.GCPInstance}
	for _, ni := range p.Search("networkInterfaces").Children() {
		subnet := fmt.Sprint(ni.Path("subnetwork").Data())
		r = append(r, v1.RelationshipResult{
			ConfigExternalID:  v1.ExternalID{ExternalID: subnet, ConfigType: v1.GCPSubnet},
			RelatedExternalID: selfExternalID,
			Relationship:      "InstanceSubnet",
		})
	}

	for _, disk := range p.Search("disks").Children() {
		diskLink := fmt.Sprint(disk.Path("source").Data())
		r = append(r, v1.RelationshipResult{
			ConfigExternalID:  selfExternalID,
			RelatedExternalID: v1.ExternalID{ExternalID: diskLink, ConfigType: v1.GCPDisk},
			Relationship:      "InstanceDisk",
		})
	}

	if cluster, exists := rd.Labels["goog-k8s-cluster-name"]; exists {
		r = append(r, v1.RelationshipResult{
			ConfigExternalID:  v1.ExternalID{ExternalID: cluster, ConfigType: v1.GCPGKECluster},
			RelatedExternalID: selfExternalID,
			Relationship:      "GKEInstance",
		})
	}
	return r
}

func resolveGCPSubnetRelationships(rd ResourceData) (r []v1.RelationshipResult) {
	selfExternalID := v1.ExternalID{ExternalID: rd.URL, ConfigType: v1.GCPSubnet}
	if network := rd.Raw.Fields["network"].GetStringValue(); network != "" {
		r = append(r, v1.RelationshipResult{
			ConfigExternalID:  v1.ExternalID{ExternalID: network, ConfigType: v1.GCPNetwork},
			RelatedExternalID: selfExternalID,
		})
	}
	return r
}

func resolveGCPGKEClusterRelationships(rd ResourceData) (r []v1.RelationshipResult) {
	selfExternalID := v1.ExternalID{ExternalID: rd.URL, ConfigType: v1.GCPGKECluster}
	if network := rd.Raw.Fields["network"].GetStringValue(); network != "" {
		r = append(r, v1.RelationshipResult{
			ConfigExternalID:  v1.ExternalID{ExternalID: network, ConfigType: v1.GCPNetwork},
			RelatedExternalID: selfExternalID,
		})
	}
	return r
}
