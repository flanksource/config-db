package gcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	compute "cloud.google.com/go/compute/apiv1"
	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	admin "cloud.google.com/go/iam/admin/apiv1"
	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	memcache "cloud.google.com/go/memcache/apiv1"
	"cloud.google.com/go/memcache/apiv1/memcachepb"
	pubsub "cloud.google.com/go/pubsub"
	redis "cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	"cloud.google.com/go/storage"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/sqladmin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

type GCPContext struct {
	api.ScrapeContext
	ProjectID string
	GKE       *container.ClusterManagerClient
	GCS       *storage.Client
	SQLAdmin  *sqladmin.Service
	IAM       *admin.IamClient
	Compute   *compute.InstancesClient
	Redis     *redis.CloudRedisClient
	Memcache  *memcache.CloudMemcacheClient
	PubSub    *pubsub.Client
	Assets    *asset.Client
}

type Scraper struct {
}

func (Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.GCP) > 0
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

	//assetClient, err := asset.NewClient(ctx, opts...)
	assetClient, err := asset.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	gkeClient, err := container.NewClusterManagerClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	sqlAdminClient, err := sqladmin.NewService(ctx)
	if err != nil {
		return nil, err
	}

	redisClient, err := redis.NewCloudRedisClient(ctx)
	if err != nil {
		return nil, err
	}

	memcacheClient, err := memcache.NewCloudMemcacheClient(ctx)
	if err != nil {
		return nil, err
	}

	pubsubClient, err := pubsub.NewClient(ctx, gcpConfig.Project)
	if err != nil {
		return nil, err
	}

	iamClient, err := admin.NewIamClient(ctx)
	if err != nil {
		return nil, err
	}

	computeClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, err
	}

	return &GCPContext{
		ProjectID: gcpConfig.Project,
		GKE:       gkeClient,
		GCS:       gcsClient,
		SQLAdmin:  sqlAdminClient,
		Redis:     redisClient,
		Memcache:  memcacheClient,
		PubSub:    pubsubClient,
		IAM:       iamClient,
		Compute:   computeClient,
		Assets:    assetClient,
	}, nil
}

func parseTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
func (gcp Scraper) gkeClusters(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("GKE") {
		return
	}

	req := &containerpb.ListClustersRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", ctx.ProjectID),
	}
	resp, err := ctx.GKE.ListClusters(ctx, req)
	if err != nil {
		results.Errorf(err, "failed to list GKE clusters")
		return
	}

	for _, cluster := range resp.Clusters {
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.GKECluster,
			CreatedAt:   parseTime(cluster.CreateTime),
			BaseScraper: config.BaseScraper,
			Config:      cluster,
			ConfigClass: "KubernetesCluster",
			Name:        cluster.Name,
			ID:          cluster.Name,
		})
	}
}

type AssetSummary struct {
	Name       string            `json:"name"`
	AssetType  string            `json:"assetType"`
	Project    string            `json:"project"`
	Location   string            `json:"location"`
	Labels     map[string]string `json:"labels,omitempty"`
	State      string            `json:"state,omitempty"`
	CreateTime string            `json:"createTime,omitempty"`
	UpdateTime string            `json:"updateTime,omitempty"`
}

func parseGCPType(assetType string) string {
	parts := strings.Split(assetType, ".googleapis.com/")
	if len(parts) != 2 {
		return "GCP::" + assetType
	}

	// compute.googleapis.com/InstanceSettings => GCP::Compute::InstanceSettings
	return fmt.Sprintf("GCP::%s::%s", lo.PascalCase(parts[0]), lo.PascalCase(parts[1]))
}

type RD struct {
	ID        string
	Name      string
	CreatedAt time.Time
	Region    string
	Zone      string
	Labels    map[string]string
	URL       string
}

func parseResourceData(data *structpb.Struct) RD {
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

	// Get createion ts, name, id
	return RD{
		ID:        data.Fields["id"].GetStringValue(),
		Name:      data.Fields["name"].GetStringValue(),
		CreatedAt: createdAt,
		Labels:    labels,
		Zone:      data.Fields["location"].GetStringValue(),
		URL:       data.Fields["selfLink"].GetStringValue(),
	}
}

func getLink(rd RD) *types.Property {
	return &types.Property{
		Name: "URL",
		// TODO: Icon
		//Icon: resourceType,
		Links: []types.Link{
			{
				Text: types.Text{Label: "Console"},
				URL:  rd.URL,
			},
		},
	}
}

func (gcp Scraper) FetchAllAssets(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {

	parent := fmt.Sprintf("projects/%s", config.Project)

	// TODO: Add support for include/exclude
	// Count should be ~416 for sandbox
	req := &assetpb.ListAssetsRequest{
		Parent:      parent,
		ContentType: assetpb.ContentType_RESOURCE,
		AssetTypes:  []string{".*.googleapis.com.*"},
	}

	fmt.Printf("Fetching all assets for project\n")

	assetCount := 0
	assetsByType := make(map[string]int)

	var results v1.ScrapeResults

	// Using any other context causes a panic
	bctx := context.Background()

	assetClient, _ := asset.NewClient(bctx)
	it := assetClient.ListAssets(bctx, req)
	for {
		asset, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing assets: %w", err)
		}

		assetCount++
		assetsByType[asset.AssetType]++

		rd := parseResourceData(asset.Resource.Data)

		// Extract basic information
		summary := extractAssetSummary(asset)

		results = append(results, v1.ScrapeResult{
			ID:          rd.ID,
			Name:        rd.Name,
			Config:      asset.Resource.Data,
			ConfigClass: parseGCPType(asset.AssetType),
			Type:        parseGCPType(asset.AssetType),
			CreatedAt:   &rd.CreatedAt,
			Labels:      rd.Labels,
			Properties:  []*types.Property{getLink(rd)},
		})

		// Print summary
		fmt.Printf("Asset #%d:\n", assetCount)
		fmt.Printf("  Name: %s\n", summary.Name)
		fmt.Printf("  Type: %s\n", summary.AssetType)
		fmt.Printf("  Project: %s\n", summary.Project)
		if summary.Location != "" {
			fmt.Printf("  Location: %s\n", summary.Location)
		}
		if summary.State != "" {
			fmt.Printf("  State: %s\n", summary.State)
		}
		if len(summary.Labels) > 0 {
			fmt.Printf("  Labels: %v\n", summary.Labels)
		}
		fmt.Println()
	}

	// Print summary
	fmt.Printf("Total Assets Found: %d\n", assetCount)
	fmt.Println("\nAssets by Type:")
	for assetType, count := range assetsByType {
		fmt.Printf("  %s: %d\n", assetType, count)
	}

	return results, nil
}

func extractAssetSummary(asset *assetpb.Asset) AssetSummary {
	summary := AssetSummary{
		Name:      asset.Name,
		AssetType: asset.AssetType,
	}

	// Extract project from resource name
	if asset.Resource != nil {
		summary.Project = extractProjectFromResource(asset.Resource.Parent)
		summary.Location = asset.Resource.Location

		// Extract labels if available
		if asset.Resource.Data != nil {
			if labels := extractLabels(asset.Resource.Data); len(labels) > 0 {
				summary.Labels = labels
			}
		}
	}

	return summary
}

func extractProjectFromResource(parent string) string {
	// Parent format: "projects/PROJECT_ID" or "//cloudresourcemanager.googleapis.com/projects/PROJECT_ID"
	if len(parent) == 0 {
		return ""
	}

	// Simple extraction - you might want to use regex for more robust parsing
	parts := strings.Split(parent, "/")
	for i, part := range parts {
		if part == "projects" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func extractLabels(data *structpb.Struct) map[string]string {
	labels := make(map[string]string)

	if data == nil || data.Fields == nil {
		return labels
	}

	// Look for labels field
	if labelsField, exists := data.Fields["labels"]; exists {
		if labelsStruct := labelsField.GetStructValue(); labelsStruct != nil {
			for key, value := range labelsStruct.Fields {
				if strValue := value.GetStringValue(); strValue != "" {
					labels[key] = strValue
				}
			}
		}
	}

	return labels
}

func (gcp Scraper) gcsBuckets(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("GCSBucket") {
		return
	}

	it := ctx.GCS.Buckets(ctx, ctx.ProjectID)
	for {
		bucketAttrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			results.Errorf(err, "failed to list GCS buckets")
			return
		}

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.GCSBucket,
			CreatedAt:   lo.ToPtr(bucketAttrs.Created),
			BaseScraper: config.BaseScraper,
			Config:      bucketAttrs,
			ConfigClass: "ObjectStorage",
			Name:        bucketAttrs.Name,
			ID:          bucketAttrs.Name,
		})
	}
}

func (gcp Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	allResults := v1.ScrapeResults{}

	for _, gcpConfig := range ctx.ScrapeConfig().Spec.GCP {
		results := v1.ScrapeResults{}
		gcpCtx, err := NewGCPContext(ctx, gcpConfig)
		if err != nil {
			results.Errorf(err, "failed to create GCP context")
			continue
		}
		assetClient, _ := asset.NewClient(gcpCtx)
		gcpCtx.Assets = assetClient

		results, err = gcp.FetchAllAssets(gcpCtx, gcpConfig)

		allResults = append(allResults, results...)
		/*
			gcp.gkeClusters(gcpCtx, gcpConfig, results)
			gcp.AuditLogs(gcpCtx, gcpConfig, results)
			gcp.iamRoles(gcpCtx, gcpConfig, results)
			gcp.iamServiceAccounts(gcpCtx, gcpConfig, results)
			gcp.gcsBuckets(gcpCtx, gcpConfig, results)
			gcp.cloudSQL(gcpCtx, gcpConfig, results)
			gcp.redisInstances(gcpCtx, gcpConfig, results)
			gcp.memcacheInstances(gcpCtx, gcpConfig, results)
			gcp.pubsubTopics(gcpCtx, gcpConfig, results)
			allResults = append(allResults, *results...)
		*/
	}

	return allResults
}

func (gcp Scraper) iamServiceAccounts(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("IAMServiceAccount") {
		return
	}

	req := &adminpb.ListServiceAccountsRequest{
		Name: fmt.Sprintf("projects/%s", ctx.ProjectID),
	}
	it := ctx.IAM.ListServiceAccounts(ctx, req)

	for {
		sa, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			results.Errorf(err, "failed to list IAM service accounts")
			return
		}

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.IAMServiceAccount,
			BaseScraper: config.BaseScraper,
			Config:      sa,
			ConfigClass: "IAM",
			Name:        sa.Name,
			ID:          sa.Name,
		})
	}
}

func (gcp Scraper) iamRoles(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("IAMRole") {
		return
	}

	resp, err := ctx.IAM.ListRoles(ctx, &adminpb.ListRolesRequest{})

	if err != nil {
		results.Errorf(err, "failed to list IAM roles")
		return
	}

	for _, role := range resp.Roles {
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.IAMRole,
			BaseScraper: config.BaseScraper,
			Config:      role,
			ConfigClass: "IAM",
			Name:        role.Name,
			ID:          role.Name,
		})
	}
}

func (gcp Scraper) cloudSQL(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("CloudSQL") {
		return
	}

	resp, err := ctx.SQLAdmin.Instances.List(config.Project).Do()
	if err != nil {
		results.Errorf(err, "failed to list Cloud SQL instances")
		return
	}

	for _, instance := range resp.Items {
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.CloudSQLInstance,
			CreatedAt:   parseTime(instance.CreateTime),
			BaseScraper: config.BaseScraper,
			Config:      instance,
			ConfigClass: "Database",
			Name:        instance.Name,
			ID:          instance.Name,
		})
	}
}

func (gcp Scraper) redisInstances(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("Redis") {
		return
	}

	it := ctx.Redis.ListInstances(ctx, &redispb.ListInstancesRequest{})

	for {
		instance, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			results.Errorf(err, "failed to list Redis instances")
			return
		}
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.RedisInstance,
			CreatedAt:   lo.ToPtr(instance.CreateTime.AsTime()),
			BaseScraper: config.BaseScraper,
			Config:      instance,
			ConfigClass: "Cache",
			Name:        instance.Name,
			ID:          instance.Name,
		})
	}
}

func (gcp Scraper) memcacheInstances(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("Memcache") {
		return
	}

	it := ctx.Memcache.ListInstances(ctx, &memcachepb.ListInstancesRequest{})

	for {
		instance, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			results.Errorf(err, "failed to list Redis instances")
			return
		}
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.MemcacheInstance,
			CreatedAt:   lo.ToPtr(instance.CreateTime.AsTime()),
			BaseScraper: config.BaseScraper,
			Config:      instance,
			ConfigClass: "Cache",
			Name:        instance.Name,
			ID:          instance.Name,
		})
	}
}

func (gcp Scraper) pubsubTopics(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("PubSub") {
		return
	}

	it := ctx.PubSub.Topics(ctx)
	for {
		topic, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			results.Errorf(err, "failed to list Pub/Sub topics")
			return
		}

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.PubSubTopic,
			BaseScraper: config.BaseScraper,
			Config:      topic,
			ConfigClass: "Messaging",
			Name:        topic.ID(),
			ID:          topic.ID(),
		})
	}
}
