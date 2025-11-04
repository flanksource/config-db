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
	"golang.org/x/oauth2/google"
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

	return ResourceData{
		ID:        id,
		Name:      getName(asset),
		CreatedAt: createdAt,
		Labels:    labels,
		URL:       selfLink,
		Zone:      strings.ToLower(zone),
		Region:    strings.ToLower(region),
		Aliases:   []string{selfLink, selfLink2},
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

var defaultIgnoreList = []string{
	"compute.googleapis.com/InstanceSettings",
	"serviceusage.googleapis.com/Service",
	"cloudkms.googleapis.com/CryptoKeyVersion",
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
			tags := results[i].Tags.AsMap()
			results[i].Aliases = append(results[i].Aliases, fmt.Sprintf("gce://%s/%s/%s", tags["project"], tags["zone"], results[i].Name))
		}
	}
	return results
}

func processResults(results v1.ScrapeResults) v1.ScrapeResults {
	results = mergeDNSRecordSetsIntoManagedZone(results)
	results = stripUnwantedFields(results)
	results = cleanLinks(results)
	results = removeTypes(results)
	results = addExtraAliases(results)
	return results
}

func (gcp Scraper) FetchAllAssets(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults

	req := &assetpb.ListAssetsRequest{
		Parent:      fmt.Sprintf("projects/%s", config.Project),
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

		rd := parseResourceData(asset)

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

		relationships := RelationshipResolver(configType, rd)
		// Add project as parent (if multiple parents are present, we use first available)
		relationships.Parents = append(relationships.Parents, v1.ConfigExternalKey{
			Type:       v1.GCPProject,
			ExternalID: config.Project,
		})

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
			Tags:                tags,
			Properties:          []*types.Property{getLink(rd)},
			RelationshipResults: relationships.Relationships,
			Children:            relationships.Children,
			Parents:             relationships.Parents,
		}

		if rd.ID != "" {
			res.Aliases = append(res.Aliases, rd.ID)
		}

		results = append(results, res)
	}

	return results, nil
}

func (Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.GCP) > 0
}

func (gcp Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	allResults := v1.ScrapeResults{}

	for _, gcpConfig := range ctx.ScrapeConfig().Spec.GCP {
		gcpCtx, err := NewGCPContext(ctx, gcpConfig)
		if err != nil {
			allResults.Errorf(err, "failed to create GCP context")
			continue
		}

		if len(gcpConfig.GetAssetTypes()) > 0 || len(gcpConfig.Include) == 0 {
			assetResults, err := gcp.FetchAllAssets(gcpCtx, gcpConfig)
			if err != nil {
				allResults.Errorf(err, "failed to fetch GCP assets")
				continue
			} else {
				allResults = append(allResults, assetResults...)
			}

			if backupResults, err := gcp.scrapeCloudSQLBackupsForAllInstances(gcpCtx, gcpConfig, assetResults); err != nil {
				allResults.Errorf(err, "failed to scrape Cloud SQL backups")
			} else {
				allResults = append(allResults, backupResults...)
			}
		}

		if gcpConfig.Includes(v1.IncludeIAMPolicy) {
			iamPolicyResults, err := gcp.FetchIAMPolicies(gcpCtx, gcpConfig)
			if err != nil {
				allResults.Errorf(err, "failed to fetch GCP IAM policies for project %s", gcpConfig.Project)
			} else {
				allResults = append(allResults, iamPolicyResults...)
			}
		}

		// Audit logs must be enabled explicitly.
		if gcpConfig.Includes(v1.IncludeAuditLogs) && len(gcpConfig.Include) > 0 {
			accessLogResults, err := gcp.FetchAuditLogs(gcpCtx, gcpConfig)
			if err != nil {
				allResults.Errorf(err, "failed to fetch GCP access logs for project %s", gcpConfig.Project)
			} else {
				allResults = append(allResults, accessLogResults...)
			}
		}

		if !gcpConfig.Excludes(v1.ExcludeSecurityCenter) {
			if analysisResults, err := gcp.ListFindings(gcpCtx, gcpConfig); err != nil {
				allResults.Errorf(err, "failed to scrape GCP Security Center findings")
			} else {
				allResults = append(allResults, analysisResults...)
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
