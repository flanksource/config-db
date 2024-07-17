package gcp

import (
	"context"
	"fmt"
	"time"

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
	"github.com/samber/lo"
	"google.golang.org/api/iterator"
	"google.golang.org/api/sqladmin/v1"
)

type GCPContext struct {
	context.Context
	ProjectID string
	GKE       *container.ClusterManagerClient
	GCS       *storage.Client
	SQLAdmin  *sqladmin.Service
	IAM       *admin.IamClient
	Compute   *compute.InstancesClient
	Redis     *redis.CloudRedisClient
	Memcache  *memcache.CloudMemcacheClient
	PubSub    *pubsub.Client
}

type Scraper struct {
}

func NewGCPContext(ctx context.Context, projectID string) (*GCPContext, error) {
	gkeClient, err := container.NewClusterManagerClient(ctx)
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

	pubsubClient, err := pubsub.NewClient(ctx, projectID)
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
		ProjectID: projectID,
		GKE:       gkeClient,
		GCS:       gcsClient,
		SQLAdmin:  sqlAdminClient,
		Redis:     redisClient,
		Memcache:  memcacheClient,
		PubSub:    pubsubClient,
		IAM:       iamClient,
		Compute:   computeClient,
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
		results := &v1.ScrapeResults{}
		gcpCtx, err := NewGCPContext(ctx, gcpConfig.Project)
		if err != nil {
			results.Errorf(err, "failed to create GCP context")
			continue
		}

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
