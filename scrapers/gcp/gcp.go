package gcp

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	"github.com/Jeffail/gabs/v2"
	"github.com/flanksource/duty/types"
	uuidV5 "github.com/gofrs/uuid/v5"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

type GCPContext struct {
	api.ScrapeContext
	ClientOpts []option.ClientOption
}

type Scraper struct{}

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
		gcpConfig.GCPConnection.Credentials = &types.EnvVar{ValueStatic: creds}
		tokenSource, err := gcpConfig.GCPConnection.TokenSource(ctx.Context,
			"https://www.googleapis.com/auth/cloud-platform",
			"https://www.googleapis.com/auth/cloud-identity.groups.readonly")
		if err != nil {
			return nil, fmt.Errorf("error getting credentials from json: %w", err)
		}
		opts = append(opts, option.WithTokenSource(tokenSource))
	}

	return &GCPContext{
		ScrapeContext: ctx,
		ClientOpts:    opts,
	}, nil
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

func parseResourceData(asset *assetpb.Asset) ResourceData {
	data := asset.Resource.Data
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

	createdAtRaw := getFieldValue(data, []string{"creationTimestamp", "createTime", "timeCreated"})
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
	selfLink := data.Fields["selfLink"].GetStringValue()
	selfLink2 := strings.TrimPrefix(selfLink, "https://www.googleapis.com/compute/v1/") // Certain references are without this prefix

	aliases := []string{selfLink, selfLink2}

	// A service account is also an IAM principal, where it is identified by
	// email rather than by resource name. The alias is what ties the config item
	// to the principal holding the grants.
	if asset.AssetType == serviceAccountAssetType {
		aliases = append(aliases, data.Fields["email"].GetStringValue())
	}

	return ResourceData{
		ID:        id,
		Name:      getName(asset),
		CreatedAt: createdAt,
		Labels:    labels,
		URL:       selfLink,
		Zone:      strings.ToLower(zone),
		Region:    strings.ToLower(region),
		Aliases:   lo.Compact(aliases),
		Raw:       data,
	}
}

func getName(asset *assetpb.Asset) string {
	name := asset.Resource.Data.Fields["name"].GetStringValue()
	if name != "" {
		return name
	}
	if asset.AssetType == "servicenetworking.googleapis.com/Connection" {
		network := asset.Resource.Data.Fields["network"].GetStringValue()
		peering := asset.Resource.Data.Fields["peering"].GetStringValue()
		service := asset.Resource.Data.Fields["service"].GetStringValue()
		name, _ = utils.Hash(network + peering + service)
	}
	return name
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

const serviceAccountAssetType = "iam.googleapis.com/ServiceAccount"

var defaultIgnoreList = []string{
	"compute.googleapis.com/InstanceSettings",
	"serviceusage.googleapis.com/Service",
	"cloudkms.googleapis.com/CryptoKeyVersion",
	// The IAM-policy scraper is the single authority for GCP::IAMRole config
	// items (created per bound role and enriched via the IAM Admin API), so the
	// role asset is suppressed here to avoid a duplicate GCP::Role config item.
	"iam.googleapis.com/Role",
}

func generateConsistentID(input string) uuid.UUID {
	gen := uuidV5.NewV5(uuidV5.NamespaceOID, input)
	return uuid.UUID(gen)
}

var unwantedFields = []string{
	"shieldedInstanceInitialState",
}

func stripUnwantedFields(results v1.ScrapeResults) v1.ScrapeResults {
	for i := range results {
		if results[i].GCPStructPB != nil {
			removeFields(results[i].GCPStructPB, unwantedFields...)
			results[i].Config = results[i].GCPStructPB
		}
	}
	return results
}

func cleanLinks(results v1.ScrapeResults) v1.ScrapeResults {
	for i := range results {
		if results[i].GCPStructPB != nil {
			applyFuncToAllStructPBStrings(results[i].GCPStructPB, func(s string) string {
				return strings.ReplaceAll(s, "https://www.googleapis.com/compute/v1/", "")
			})
			results[i].Config = results[i].GCPStructPB
		}
	}
	return results
}

var typesToRemove = []string{
	v1.GCPBackup,
	v1.GCPBackupRun,
}

func removeTypes(results v1.ScrapeResults) v1.ScrapeResults {
	var newResults v1.ScrapeResults
	for _, r := range results {
		if !slices.Contains(typesToRemove, r.Type) {
			newResults = append(newResults, r)
		}
	}
	return newResults
}

func addExtraAliases(results v1.ScrapeResults) v1.ScrapeResults {
	for i := range results {
		if results[i].Type == v1.GCPInstance {
			tags := results[i].Tags
			results[i].Aliases = append(results[i].Aliases, fmt.Sprintf("gce://%s/%s/%s", tags["project"], tags["zone"], results[i].Name))
		}
	}
	return results
}

func processResults(results v1.ScrapeResults) v1.ScrapeResults {
	results = coalesceIAMRoleConfigs(results)
	results = mergeDNSRecordSetsIntoManagedZone(results)
	results = stripUnwantedFields(results)
	results = cleanLinks(results)
	results = removeTypes(results)
	results = addExtraAliases(results)
	return results
}

// FetchAllAssets lists every asset beneath parent, which is either a single
// project or a whole organization.
func (gcp Scraper) FetchAllAssets(ctx *GCPContext, config v1.GCP, parent string) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults

	req := &assetpb.ListAssetsRequest{
		Parent:      parent,
		ContentType: assetpb.ContentType_RESOURCE,
		AssetTypes:  []string{".*.googleapis.com.*"},
		PageSize:    1000,
	}

	if assetTypes := config.GetAssetTypes(); len(assetTypes) > 0 {
		req.AssetTypes = assetTypes
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

	ignoreList := append(defaultIgnoreList, config.Exclude...)

	// Ancestry is resolved after the listing completes: a project's own asset can
	// arrive after the assets it owns, so the number→id mapping is only complete
	// once every asset has streamed past.
	resolver := newProjectResolver(projectFromParent(parent))
	var ancestries []assetAncestry

	it := assetClient.ListAssets(ctx, req)
	for {
		asset, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing assets: %w", err)
		}

		resolver.record(asset)

		if lo.Contains(ignoreList, asset.AssetType) {
			continue
		}

		rd := parseResourceData(asset)

		configClass := parseGCPConfigClass(asset.AssetType)
		configType := fmt.Sprintf("GCP::%s", configClass)

		var tags []v1.Tag

		region := rd.Region
		if region != "" {
			tags = append(tags, v1.Tag{Name: "region", Value: region})
		}

		if rd.Zone != "" {
			tags = append(tags, v1.Tag{Name: "zone", Value: rd.Zone})
		}

		relationships := RelationshipResolver(configType, rd)

		res := v1.ScrapeResult{
			BaseScraper:         config.BaseScraper,
			ID:                  lo.CoalesceOrEmpty(rd.ID, rd.Name),
			Name:                rd.Name,
			Aliases:             append(rd.Aliases, asset.Name),
			Config:              asset.Resource.Data,
			GCPStructPB:         asset.Resource.Data,
			ConfigClass:         configClass,
			Type:                configType,
			CreatedAt:           lo.ToPtr(rd.CreatedAt),
			Labels:              rd.Labels,
			Tags:                v1.Tags(tags).AsMap(),
			Properties:          []*types.Property{getLink(rd)},
			RelationshipResults: relationships.Relationships,
			Children:            relationships.Children,
			Parents:             relationships.Parents,
		}

		if rd.ID != "" {
			res.Aliases = append(res.Aliases, rd.ID)
		}

		results = append(results, res)
		ancestries = append(ancestries, assetAncestry{
			ancestors:       asset.Ancestors,
			isHierarchyNode: isResourceManagerNode(asset.AssetType),
		})
	}

	for i := range results {
		ancestry := ancestries[i]

		if project := resolver.resolve(ancestry.ancestors); project != "" {
			if results[i].Tags == nil {
				results[i].Tags = map[string]string{}
			}
			results[i].Tags["project"] = project

			if !ancestry.isHierarchyNode {
				// Add project as parent (if multiple parents are present, we use first available)
				results[i].Parents = append(results[i].Parents, v1.ConfigExternalKey{
					Type:       v1.GCPProject,
					ExternalID: project,
				})
			}
		}

		// Organizations, folders and projects hang off the hierarchy rather than
		// off a project, so their parent comes straight from their ancestry.
		if ancestry.isHierarchyNode {
			parent, err := resourceManagerParent(ancestry.ancestors)
			if err != nil {
				ctx.Warnf("gcp assets: %v", err)
			} else if parent != nil {
				results[i].Parents = append(results[i].Parents, *parent)
			}
		}
	}

	return results, nil
}

// scrapeResourceHierarchy emits the project, folder and organization config items without
// the surrounding IAM pass.
func (Scraper) scrapeResourceHierarchy(ctx *GCPContext, config v1.GCP, parent string) (v1.ScrapeResults, error) {
	hierarchy, err := fetchResourceManagerHierarchy(ctx, config, parent)
	if err != nil {
		return nil, err
	}
	results, _, err := buildResourceManagerHierarchy(hierarchy.Project, hierarchy.Nodes, config.BaseScraper)
	return results, err
}

func (Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.GCP) > 0
}

// parentScrapers holds the passes scrapeParent runs, so which of them the include list
// reaches can be exercised without a live GCP project behind each one.
type parentScrapers struct {
	fetchAssets      func(*GCPContext, v1.GCP, string) (v1.ScrapeResults, error)
	fetchSQLBackups  func(*GCPContext, v1.GCP, string, v1.ScrapeResults) (v1.ScrapeResults, error)
	fetchHierarchy   func(*GCPContext, v1.GCP, string) (v1.ScrapeResults, error)
	fetchIAMPolicies func(*GCPContext, v1.GCP, string) (iamPolicyResult, error)
	fetchGroups      func(*GCPContext, v1.GCP, iamScope, []string) (v1.ScrapeResults, error)
}

// scrapeParent runs the passes that are scoped to one asset-inventory root.
func (gcp Scraper) scrapeParent(ctx *GCPContext, config v1.GCP, parent string) v1.ScrapeResults {
	return gcp.scrapeParentWith(ctx, config, parent, parentScrapers{
		fetchAssets:      gcp.FetchAllAssets,
		fetchSQLBackups:  gcp.scrapeCloudSQLBackupsForAllInstances,
		fetchHierarchy:   gcp.scrapeResourceHierarchy,
		fetchIAMPolicies: gcp.FetchIAMPolicies,
		fetchGroups:      gcp.FetchGroupMemberships,
	})
}

func (gcp Scraper) scrapeParentWith(ctx *GCPContext, config v1.GCP, parent string, scrapers parentScrapers) v1.ScrapeResults {
	var results v1.ScrapeResults

	if len(config.GetAssetTypes()) > 0 || len(config.Include) == 0 {
		assetResults, err := scrapers.fetchAssets(ctx, config, parent)
		if err != nil {
			results.Errorf(err, "failed to fetch GCP assets for %s", parent)
			return results
		}
		results = append(results, assetResults...)

		if backupResults, err := scrapers.fetchSQLBackups(ctx, config, parent, assetResults); err != nil {
			results.Errorf(err, "failed to scrape Cloud SQL backups for %s", parent)
		} else {
			results = append(results, backupResults...)
		}
	}

	if !config.Includes(v1.IncludeIAMPolicy) {
		// The IAM pass reads the same hierarchy for grant scoping, so this runs only when
		// that pass does not. The project item anchors every asset's parent edge and is the
		// root unresolved spend is booked against, so it has to exist whatever the include
		// list narrows the scrape to.
		if hierarchyResults, err := scrapers.fetchHierarchy(ctx, config, parent); err != nil {
			results.Errorf(err, "failed to read the GCP resource hierarchy above %s, its project, folder and organization config items will be missing", parent)
		} else {
			results = append(results, hierarchyResults...)
		}
	}

	if config.Includes(v1.IncludeIAMPolicy) {
		iamPolicy, err := scrapers.fetchIAMPolicies(ctx, config, parent)
		if err != nil {
			results.Errorf(err, "failed to fetch GCP IAM policies for %s", parent)
			return results
		}
		results = append(results, iamPolicy.Results...)

		// Group-membership expansion runs by default alongside IAM policy so
		// group grants unwrap to their members. It needs the Cloud Identity
		// groups.readonly scope; disable with exclude: [IAMGroupMembers] when
		// the scrape service account lacks it.
		if config.Includes(v1.IncludeGroupMembers) && !config.Excludes(v1.IncludeGroupMembers) {
			memberResults, err := scrapers.fetchGroups(ctx, config, iamPolicy.Scope, iamPolicy.GroupEmails)
			if err != nil {
				results.Errorf(err, "failed to fetch GCP group memberships for %s", parent)
			} else {
				results = append(results, memberResults...)
			}
		}
	}

	return results
}

// auditLogProject returns the project holding the audit-log dataset: the one
// configured explicitly, or the sole scraped project when it is unambiguous.
func auditLogProject(config v1.GCP) string {
	if config.AuditLogs.Project != "" {
		return strings.TrimPrefix(config.AuditLogs.Project, v1.ProjectPrefix)
	}
	if projects := config.ConfiguredProjects(); len(projects) == 1 {
		return projects[0]
	}
	return ""
}

func (gcp Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	allResults := v1.ScrapeResults{}

	for _, gcpConfig := range ctx.ScrapeConfig().Spec.GCP {
		if err := gcpConfig.Validate(); err != nil {
			allResults.Errorf(err, "invalid GCP scraper config")
			continue
		}

		if !gcpConfig.IsOrgScoped() {
			ctx.Warnf("gcp: no organization configured for %s, identities are tenanted by project", gcpConfig.Scope())
		}

		gcpCtx, err := NewGCPContext(ctx, gcpConfig)
		if err != nil {
			allResults.Errorf(err, "failed to create GCP context")
			continue
		}

		parents, err := resolveParents(gcpCtx, gcpConfig)
		if err != nil {
			allResults.Errorf(err, "failed to resolve GCP scrape scope")
			continue
		}
		if len(parents) == 0 {
			ctx.Warnf("gcp: nothing to scrape for %s", gcpConfig.Scope())
			continue
		}

		for _, parent := range parents {
			allResults = append(allResults, gcp.scrapeParent(gcpCtx, gcpConfig, parent)...)
		}

		// Security Center follows the resolved roots directly: one organization
		// listing for an unrestricted organization, or one per selected project.
		if !gcpConfig.Excludes(v1.ExcludeSecurityCenter) {
			for _, parent := range parents {
				if analysisResults, err := gcp.ListFindings(gcpCtx, parent); err != nil {
					allResults.Errorf(err, "failed to scrape GCP Security Center findings for %s", parent)
				} else {
					allResults = append(allResults, analysisResults...)
				}
			}
		}

		// Audit logs must be enabled explicitly. The dataset lives in a single
		// project, so it is read once rather than per scraped project.
		if gcpConfig.Includes(v1.IncludeAuditLogs) && len(gcpConfig.Include) > 0 {
			if project := auditLogProject(gcpConfig); project == "" {
				ctx.Warnf("gcp: skipping audit logs for %s, set auditLogs.project to the project holding the dataset", gcpConfig.Scope())
			} else if accessLogResults, err := gcp.FetchAuditLogs(gcpCtx, gcpConfig, project, parents); err != nil {
				allResults.Errorf(err, "failed to fetch GCP access logs for project %s", project)
			} else {
				allResults = append(allResults, accessLogResults...)
			}
		}
	}

	return processResults(allResults)
}

type relationshipResults struct {
	Parents       []v1.ConfigExternalKey
	Children      []v1.ConfigExternalKey
	Relationships []v1.RelationshipResult
}

func RelationshipResolver(assetType string, rd ResourceData) relationshipResults {
	switch assetType {
	case v1.GCPInstance:
		return resolveGCPInstanceRelationships(rd)
	case v1.GCPSubnet:
		return resolveGCPSubnetRelationships(rd)
	case v1.GCPGKECluster:
		return resolveGCPGKEClusterRelationships(rd)
	}
	return relationshipResults{}
}

func resolveGCPInstanceRelationships(rd ResourceData) (r relationshipResults) {
	data := rd.Raw
	b, _ := data.MarshalJSON()
	p, _ := gabs.ParseJSON(b)
	selfExternalID := v1.ExternalID{ExternalID: data.Fields["selfLink"].GetStringValue(), ConfigType: v1.GCPInstance}
	for _, ni := range p.Search("networkInterfaces").Children() {
		subnet := fmt.Sprint(ni.Path("subnetwork").Data())
		r.Parents = append(r.Parents, v1.ConfigExternalKey{
			ExternalID: subnet,
			Type:       v1.GCPSubnet,
			ScraperID:  "all",
		})
	}

	for _, disk := range p.Search("disks").Children() {
		diskLink := fmt.Sprint(disk.Path("source").Data())
		r.Relationships = append(r.Relationships, v1.RelationshipResult{
			ConfigExternalID:  selfExternalID,
			RelatedExternalID: v1.ExternalID{ExternalID: diskLink, ConfigType: v1.GCPDisk},
			Relationship:      "InstanceDisk",
		})
	}

	if clusterIDBase32, exists := rd.Labels["goog-gke-cluster-id-base32"]; exists {
		if clusterID, _ := utils.Base32ToString(clusterIDBase32); clusterID != "" {
			r.Relationships = append(r.Relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: clusterID, ConfigType: v1.GCPGKECluster},
				RelatedExternalID: selfExternalID,
				Relationship:      "GKEInstance",
			})
		}
	}
	return r
}

func resolveGCPSubnetRelationships(rd ResourceData) (r relationshipResults) {
	if network := rd.Raw.Fields["network"].GetStringValue(); network != "" {
		r.Parents = append(r.Parents, v1.ConfigExternalKey{
			ExternalID: network,
			Type:       v1.GCPNetwork,
			ScraperID:  "all",
		})
	}
	return r
}

func resolveGCPGKEClusterRelationships(rd ResourceData) (r relationshipResults) {
	if network := rd.Raw.Fields["network"].GetStringValue(); network != "" {
		r.Parents = append(r.Parents, v1.ConfigExternalKey{
			ExternalID: network,
			Type:       v1.GCPNetwork,
			ScraperID:  "all",
		})
	}
	return r
}
