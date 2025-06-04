package gcp

import (
	"fmt"
	"strings"
	"time"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/structpb"
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
	defer assetClient.Close()

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
	defer assetClient.Close()

	it := assetClient.ListAssets(ctx, req)
	for {
		assetItem, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing IAM policies: %w", err)
		}

		fmt.Printf("\n\nIAM Policy: %+v\n", assetItem.Name)
		if assetItem.IamPolicy == nil {
			continue
		}

		for _, binding := range assetItem.IamPolicy.Bindings {
			fmt.Printf("Binding Role: %+v\n", binding.Role)
			for _, member := range binding.Members {
				fmt.Printf("Member: %+v\n", member)
				// Extract a more user-friendly name if member is like "user:email@example.com"
				memberName := member
				if strings.HasPrefix(member, "user:") {
					memberName = strings.TrimPrefix(member, "user:")
				} else if strings.HasPrefix(member, "serviceAccount:") {
					memberName = strings.TrimPrefix(member, "serviceAccount:")
				} else if strings.HasPrefix(member, "group:") {
					memberName = strings.TrimPrefix(member, "group:")
				}

				bindingID := fmt.Sprintf("projects/%s/%s/%s", config.Project, member, binding.Role)

				results = append(results, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					ID:          bindingID,
					Name:        memberName, // Use the extracted member name
					Config: map[string]string{
						"member": member,
						"role":   binding.Role,
					},
					ConfigClass: "IAMPrincipalBinding",
					Type:        "GCP::IAMPrincipalBinding",
					Tags:        []v1.Tag{{Name: "project", Value: config.Project}},
				})
			}
		}
	}

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
	}

	return allResults
}
