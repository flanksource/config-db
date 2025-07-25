package aws

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/support"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/samber/lo"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

// Scraper ...
type Scraper struct {
}

type AWSContext struct {
	api.ScrapeContext
	Session *aws.Config
	STS     *sts.Client
	EC2     *ec2.Client
	IAM     *iam.Client
	Caller  *sts.GetCallerIdentityOutput
	Support *support.Client
	SSM     *ssm.Client
	Config  *configservice.Client
	Subnets map[string]Zone
}

const (
	IncludeRDSEvents = "RDSEvents"
)

// Config changes sources
const (
	SourceRDSEvents = "RDS Events"
	SourceAWSBackup = "AWS Backup"
)

func getLabels(tags []ec2Types.Tag) v1.JSONStringMap {
	result := make(v1.JSONStringMap)
	for _, tag := range tags {
		result[*tag.Key] = *tag.Value
	}
	return result
}

func (ctx AWSContext) String() string {
	return fmt.Sprintf("account=%s user=%s region=%s", lo.FromPtr(ctx.Caller.Account), *ctx.Caller.UserId, ctx.Session.Region)
}

// Function to return an endpoint resolver based on input options type
func getEndpointResolver[T any](awsConfig v1.AWS) func(o *T) {
	var val *string
	if awsConfig.Endpoint != "" {
		val = lo.ToPtr(awsConfig.Endpoint)
	}
	return func(o *T) {
		switch opts := any(o).(type) {
		case *backup.Options:
			opts.BaseEndpoint = val
		case *ec2.Options:
			opts.BaseEndpoint = val
		case *iam.Options:
			opts.BaseEndpoint = val
		case *ssm.Options:
			opts.BaseEndpoint = val
		case *sns.Options:
			opts.BaseEndpoint = val
		case *ecr.Options:
			opts.BaseEndpoint = val
		case *sqs.Options:
			opts.BaseEndpoint = val
		case *cloudformation.Options:
			opts.BaseEndpoint = val
		case *ecs.Options:
			opts.BaseEndpoint = val
		case *elasticache.Options:
			opts.BaseEndpoint = val
		case *lambda.Options:
			opts.BaseEndpoint = val
		case *eks.Options:
			opts.BaseEndpoint = val
		case *efs.Options:
			opts.BaseEndpoint = val
		case *rds.Options:
			opts.BaseEndpoint = val
		case *s3.Options:
			opts.BaseEndpoint = val
		case *route53.Options:
			opts.BaseEndpoint = val
		case *sts.Options:
			opts.BaseEndpoint = val
		case *elasticloadbalancing.Options:
			opts.BaseEndpoint = val
		case *elasticloadbalancingv2.Options:
			opts.BaseEndpoint = val
		case *cloudtrail.Options:
			opts.BaseEndpoint = val
		default:
			logger.Errorf("unsupported type for resolver endpoint: %T", o)
		}
	}
}

func (aws Scraper) getContext(ctx api.ScrapeContext, awsConfig v1.AWS, region string) (*AWSContext, error) {
	awsConn := awsConfig.AWSConnection.ToDutyAWSConnection(region)
	if err := awsConn.Populate(ctx); err != nil {
		return nil, err
	}

	session, err := awsConn.Client(ctx.Context)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session for region=%q: %w", region, err)
	}

	STS := sts.NewFromConfig(session, getEndpointResolver[sts.Options](awsConfig))
	caller, err := STS.GetCallerIdentity(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get identity for region=%q: %w", region, err)
	}

	usEast1 := session.Copy()
	usEast1.Region = "us-east-1"

	return &AWSContext{
		ScrapeContext: ctx,
		Session:       &session,
		Caller:        caller,
		STS:           STS,
		Support:       support.NewFromConfig(usEast1),
		EC2:           ec2.NewFromConfig(session, getEndpointResolver[ec2.Options](awsConfig)),
		SSM:           ssm.NewFromConfig(session, getEndpointResolver[ssm.Options](awsConfig)),
		IAM:           iam.NewFromConfig(session, getEndpointResolver[iam.Options](awsConfig)),
		Subnets:       make(map[string]Zone),
		Config:        configservice.NewFromConfig(session),
	}, nil
}

func strPtr(s string) *string {
	return &s
}

func getName(tags v1.JSONStringMap, def string) string {
	if name, ok := tags["Name"]; ok {
		return name
	}
	return def
}

type Zone struct {
	Region, Zone, ZoneID string
}

func (aws Scraper) containerImages(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("ECR") {
		return
	}

	ctx.Logger.V(2).Infof("scraping ECR")

	ECR := ecr.NewFromConfig(*ctx.Session, getEndpointResolver[ecr.Options](config))
	images, err := ECR.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{})
	if err != nil {
		results.Errorf(err, "failed to get ecr")
		return
	}
	labels := make(map[string]string)
	for _, image := range images.Repositories {
		*results = append(*results, v1.ScrapeResult{
			CreatedAt:   image.CreatedAt,
			Type:        "AWS::ECR::Repository",
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, "AWS::ECR::Repository", lo.FromPtr(image.RepositoryName), nil)},
			Config:      image,
			Labels:      labels,
			ConfigClass: "ContainerRegistry",
			Name:        *image.RepositoryName,
			Aliases:     []string{*image.RepositoryArn, "AmazonECR/" + *image.RepositoryArn},
			ID:          *image.RepositoryUri,
			Ignore: []string{
				"CreatedAt", "RepositoryArn", "RepositoryUri", "RegistryId", "RepositoryName",
			},
			Parents: []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) sqs(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("sqs") {
		return
	}

	ctx.Logger.V(2).Infof("scraping SQS queues")
	client := sqs.NewFromConfig(*ctx.Session, getEndpointResolver[sqs.Options](config))
	listQueuesOutput, err := client.ListQueues(ctx, &sqs.ListQueuesInput{})
	if err != nil {
		results.Errorf(err, "failed to list SQS queues")
		return
	}

	for _, queueURL := range listQueuesOutput.QueueUrls {
		getQueueAttributesOutput, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       &queueURL,
			AttributeNames: []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameAll},
		})
		if err != nil {
			results.Errorf(err, "failed to get attributes for SQS queue: %s", queueURL)
			continue
		}

		createdTimestamp, err := strconv.ParseInt(getQueueAttributesOutput.Attributes["CreatedTimestamp"], 10, 64)
		if err != nil {
			results.Errorf(err, "Failed to parse creation timestamp for queue: %s", queueURL)
			continue
		}

		queueName := queueURL[strings.LastIndex(queueURL, "/")+1:]
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSSQS,
			Aliases:     []string{getQueueAttributesOutput.Attributes["QueueArn"]},
			CreatedAt:   lo.ToPtr(time.Unix(createdTimestamp, 0)),
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSSQS, queueURL, nil)},
			Config:      unwrapFields(getQueueAttributesOutput.Attributes, "Policy"),
			Labels:      getQueueAttributesOutput.Attributes,
			ConfigClass: "Queue",
			Name:        queueName,
			ID:          queueURL,
		})
	}
}

func (aws Scraper) cloudformationStacks(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("cloudformation") {
		return
	}

	ctx.Logger.V(2).Infof("scraping CloudFormation stacks")

	client := cloudformation.NewFromConfig(*ctx.Session, getEndpointResolver[cloudformation.Options](config))
	stacks, err := client.ListStacks(ctx, &cloudformation.ListStacksInput{})
	if err != nil {
		results.Errorf(err, "failed to list CloudFormation stacks")
		return
	}

	for _, stack := range stacks.StackSummaries {
		stackName := lo.FromPtr(stack.StackName)

		*results = append(*results, v1.ScrapeResult{
			Type:         v1.AWSCloudFormationStack,
			BaseScraper:  config.BaseScraper,
			Properties:   []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSCloudFormationStack, stackName, nil)},
			Config:       stack,
			Status:       string(stack.StackStatus),
			CreatedAt:    stack.CreationTime,
			DeletedAt:    stack.DeletionTime,
			DeleteReason: v1.DeletedReasonFromAttribute,
			ConfigClass:  "Stack",
			Name:         stackName,
			ID:           *stack.StackId,
			Aliases:      []string{lo.FromPtr(stack.StackId)},
			Parents:      []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) snsTopics(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("sns") {
		return
	}

	ctx.Logger.V(2).Infof("scraping SNS topics")
	client := sns.NewFromConfig(*ctx.Session, getEndpointResolver[sns.Options](config))
	topics, err := client.ListTopics(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to list SNS topics")
		return
	}

	for _, topic := range topics.Topics {
		topicArn := lo.FromPtr(topic.TopicArn)
		labels := make(map[string]string)
		labels["region"] = ctx.Session.Region

		attributeOutput, err := client.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{
			TopicArn: topic.TopicArn,
		})
		if err != nil {
			ctx.Logger.Errorf("failed to get attributes for topic %s: %v", topicArn, err)
			continue
		}

		topicName := topicArn[strings.LastIndex(topicArn, ":")+1:]
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSSNSTopic,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSSNSTopic, topicArn, nil)},
			Config:      unwrapFields(attributeOutput.Attributes, "Policy", "EffectiveDeliveryPolicy"),
			Labels:      labels,
			ConfigClass: "Topic",
			Name:        topicName,
			Aliases:     []string{topicArn},
			ID:          topicArn,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) ecsClusters(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("ecs") {
		return
	}

	ctx.Logger.V(2).Infof("scraping ECS clusters")
	client := ecs.NewFromConfig(*ctx.Session, getEndpointResolver[ecs.Options](config))
	clusters, err := client.ListClusters(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to list ECS clusters")
		return
	}

	for _, clusterArn := range clusters.ClusterArns {
		cluster, err := client.DescribeClusters(ctx, &ecs.DescribeClustersInput{
			Clusters: []string{clusterArn},
		})
		if err != nil {
			results.Errorf(err, "failed to describe ECS cluster")
			continue
		}

		for _, clusterInfo := range cluster.Clusters {
			labels := make(map[string]string)
			for _, tag := range clusterInfo.Tags {
				labels[*tag.Key] = *tag.Value
			}

			*results = append(*results, v1.ScrapeResult{
				Type:        v1.AWSECSCluster,
				Labels:      labels,
				BaseScraper: config.BaseScraper,
				Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSECSCluster, *clusterInfo.ClusterName, nil)},
				Config:      clusterInfo,
				ConfigClass: "ECSCluster",
				Name:        *clusterInfo.ClusterName,
				Aliases:     []string{clusterArn},
				ID:          clusterArn,
			})

			aws.ecsTasks(ctx, config, client, clusterArn, *clusterInfo.ClusterName, results)
			aws.ecsServices(ctx, config, client, *clusterInfo.ClusterArn, *clusterInfo.ClusterName, results)
		}
	}
}

func (aws Scraper) ecsServices(ctx *AWSContext, config v1.AWS, client *ecs.Client, cluster, clusterName string, results *v1.ScrapeResults) {
	if !config.Includes("ECSService") {
		return
	}

	ctx.Logger.V(2).Infof("scraping ECS services")

	services, err := client.ListServices(ctx, &ecs.ListServicesInput{
		Cluster: &cluster,
	})
	if err != nil {
		results.Errorf(err, "failed to list ECS services in cluster %s", cluster)
		return
	}

	if len(services.ServiceArns) == 0 {
		return
	}

	describeServicesOutput, err := client.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  &cluster,
		Services: services.ServiceArns,
	})
	if err != nil {
		results.Errorf(err, "failed to describe ECS services in cluster %s", cluster)
		return
	}

	for _, service := range describeServicesOutput.Services {
		var relationships []v1.RelationshipResult
		// ECS Task Definition to ECS Service relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: v1.ExternalID{ExternalID: *service.ServiceArn, ConfigType: v1.AWSECSService},
			ConfigExternalID:  v1.ExternalID{ExternalID: *service.TaskDefinition, ConfigType: v1.AWSECSTaskDefinition},
			Relationship:      "ECSTaskDefinitionECSService",
		})

		labels := make(map[string]string)
		for _, tag := range service.Tags {
			labels[*tag.Key] = *tag.Value
		}

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSECSService,
			ID:                  *service.ServiceArn,
			Name:                *service.ServiceName,
			Config:              service,
			RelationshipResults: relationships,
			ConfigClass:         "ECSService",
			BaseScraper:         config.BaseScraper,
			Labels:              labels,
			Status:              formatStatus(lo.FromPtr(service.Status)),
			CreatedAt:           service.CreatedAt,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSECSService, *service.ServiceName, map[string]string{"cluster": clusterName})},
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSECSCluster, ExternalID: cluster}},
		})
	}
}

func (aws Scraper) listAllECSTasks(ctx *AWSContext, client *ecs.Client, clusterArn string, results *v1.ScrapeResults) ([]string, error) {
	var taskArns []string
	for _, desiredStatus := range ecsTypes.DesiredStatus("").Values() {
		ctx.Logger.V(2).Infof("scraping %s ECS tasks for cluster %s", desiredStatus, clusterArn)

		for {
			tasks, err := client.ListTasks(ctx, &ecs.ListTasksInput{
				Cluster:       &clusterArn,
				DesiredStatus: desiredStatus,
			})
			if err != nil {
				results.Errorf(err, "failed to list ECS tasks in cluster %s", clusterArn)
				continue
			}

			taskArns = append(taskArns, tasks.TaskArns...)
			if tasks.NextToken == nil {
				break
			}
		}
	}

	return taskArns, nil
}

func (aws Scraper) ecsTasks(ctx *AWSContext, config v1.AWS, client *ecs.Client, clusterArn, clusterName string, results *v1.ScrapeResults) {
	if !config.Includes("ECSTask") {
		return
	}

	allTaskArns, err := aws.listAllECSTasks(ctx, client, clusterArn, results)
	if err != nil {
		results.Errorf(err, "failed to list ECS tasks in cluster %s", clusterArn)
		return
	}

	for _, taskArns := range lo.Chunk(allTaskArns, 100) {
		ctx.Logger.V(2).Infof("describing %d ECS tasks for cluster %s", len(allTaskArns), clusterArn)

		describeTasksOutput, err := client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
			Cluster: &clusterArn,
			Tasks:   taskArns,
		})
		if err != nil {
			results.Errorf(err, "failed to describe ECS tasks in cluster %s", clusterArn)
			continue
		}

		if len(describeTasksOutput.Failures) > 0 {
			results.Errorf(fmt.Errorf("%v", describeTasksOutput.Failures), "failed to describe ECS tasks in cluster %s", clusterArn)
			continue
		}

		for _, task := range describeTasksOutput.Tasks {
			taskID := strings.Split(*task.TaskArn, "/")[len(strings.Split(*task.TaskArn, "/"))-1]

			var name string
			labels := make(map[string]string)
			for _, tag := range task.Tags {
				if strings.ToLower(*tag.Key) == "name" {
					name = *tag.Value
				} else {
					labels[*tag.Key] = *tag.Value
				}
			}

			if name == "" {
				name = strings.TrimPrefix(lo.FromPtr(task.Group), "family:")
				name = strings.TrimPrefix(name, "service:")
			}

			*results = append(*results, v1.ScrapeResult{
				Type:        v1.AWSECSTask,
				ID:          *task.TaskArn,
				Name:        name,
				Config:      task,
				Labels:      labels,
				ConfigClass: "ECSTask",
				CreatedAt:   task.CreatedAt,
				Health:      models.Health(strings.ToLower(string(task.HealthStatus))),
				DeletedAt:   task.ExecutionStoppedAt,
				Status:      formatStatus(*task.LastStatus),
				BaseScraper: config.BaseScraper,
				Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSECSTask, taskID, map[string]string{"cluster": clusterName})},
				RelationshipResults: []v1.RelationshipResult{
					{
						ConfigExternalID:  v1.ExternalID{ExternalID: *task.TaskDefinitionArn, ConfigType: v1.AWSECSTaskDefinition},
						RelatedExternalID: v1.ExternalID{ExternalID: *task.TaskArn, ConfigType: v1.AWSECSTask},
						Relationship:      "ECSTaskDefinitionECSTask",
					},
				},
				Parents: []v1.ConfigExternalKey{
					{Type: v1.AWSECSCluster, ExternalID: clusterArn},
				},
			})
		}
	}
}

func (aws Scraper) ecsTaskDefinitions(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("ECSTaskDefinition") {
		return
	}

	ctx.Logger.V(2).Infof("scraping ECS task definitions")
	client := ecs.NewFromConfig(*ctx.Session, getEndpointResolver[ecs.Options](config))
	var tdStatus ecsTypes.TaskDefinitionStatus
	for _, status := range tdStatus.Values() {
		ctx.Logger.V(3).Infof("scraping %s ECS task definitions", status)

		input := &ecs.ListTaskDefinitionsInput{Status: status}
		paginator := ecs.NewListTaskDefinitionsPaginator(client, input)

		for paginator.HasMorePages() {
			output, err := paginator.NextPage(ctx)
			if err != nil {
				results.Errorf(err, "failed to list ECS tasks")
				break
			}

			if len(output.TaskDefinitionArns) == 0 {
				break
			}

			for _, taskDefinitionArn := range output.TaskDefinitionArns {
				describeTaskDefinitionOutput, err := client.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
					TaskDefinition: &taskDefinitionArn,
				})
				if err != nil {
					results.Errorf(err, "failed to describe ECS task definition %s", taskDefinitionArn)
					continue
				}

				labels := make(map[string]string)
				for _, tag := range describeTaskDefinitionOutput.Tags {
					labels[*tag.Key] = *tag.Value
				}

				*results = append(*results, v1.ScrapeResult{
					Type:        v1.AWSECSTaskDefinition,
					Labels:      labels,
					CreatedAt:   describeTaskDefinitionOutput.TaskDefinition.RegisteredAt,
					DeletedAt:   describeTaskDefinitionOutput.TaskDefinition.DeregisteredAt,
					ID:          *describeTaskDefinitionOutput.TaskDefinition.TaskDefinitionArn,
					Name:        *describeTaskDefinitionOutput.TaskDefinition.Family,
					Config:      describeTaskDefinitionOutput.TaskDefinition,
					Status:      formatStatus(string(describeTaskDefinitionOutput.TaskDefinition.Status)),
					ConfigClass: "ECSTaskDefinition",
					BaseScraper: config.BaseScraper,
					Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSECSTaskDefinition, *describeTaskDefinitionOutput.TaskDefinition.Family, nil)},
				})
			}
		}
	}
}

func (aws Scraper) eksFargateProfiles(ctx *AWSContext, config v1.AWS, client *eks.Client, clusterName string, results *v1.ScrapeResults) {
	if !config.Includes("FargateProfile") {
		return
	}

	ctx.Logger.V(2).Infof("scraping EKS Fargate profiles")

	listFargateProfilesInput := &eks.ListFargateProfilesInput{
		ClusterName: &clusterName,
	}
	listFargateProfilesOutput, err := client.ListFargateProfiles(ctx, listFargateProfilesInput)
	if err != nil {
		results.Errorf(err, "failed to list Fargate profiles for cluster %s", clusterName)
		return
	}

	for _, profileName := range listFargateProfilesOutput.FargateProfileNames {
		describeFargateProfileInput := &eks.DescribeFargateProfileInput{
			ClusterName:        &clusterName,
			FargateProfileName: &profileName,
		}
		describeFargateProfileOutput, err := client.DescribeFargateProfile(ctx, describeFargateProfileInput)
		if err != nil {
			results.Errorf(err, "failed to describe Fargate profile %s for cluster %s", profileName, clusterName)
			continue
		}

		*results = append(*results, v1.ScrapeResult{
			ID:          *describeFargateProfileOutput.FargateProfile.FargateProfileName,
			Type:        v1.AWSEKSFargateProfile,
			BaseScraper: config.BaseScraper,
			Config:      describeFargateProfileOutput.FargateProfile,
			ConfigClass: "FargateProfile",
			Tags:        []v1.Tag{{Name: "cluster", Value: clusterName}},
			Name:        *describeFargateProfileOutput.FargateProfile.FargateProfileName,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSEKSCluster, ExternalID: clusterName}},
		})
	}
}

func (aws Scraper) elastiCache(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("ElastiCache") {
		return
	}

	ctx.Logger.V(2).Infof("scraping elastiCache")
	svc := elasticache.NewFromConfig(*ctx.Session, getEndpointResolver[elasticache.Options](config))

	clusters, err := svc.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{})
	if err != nil {
		results.Errorf(err, "failed to describe ElastiCache clusters")
		return
	}

	for _, cluster := range clusters.CacheClusters {
		*results = append(*results, v1.ScrapeResult{
			ID:          *cluster.CacheClusterId,
			Type:        v1.AWSElastiCacheCluster,
			BaseScraper: config.BaseScraper,
			Config:      cluster,
			ConfigClass: "Cache",
			Name:        *cluster.CacheClusterId,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) lambdaFunctions(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("lambda") {
		return
	}
	ctx.Logger.V(2).Infof("scraping lambda functions")
	lambdaClient := lambda.NewFromConfig(*ctx.Session, getEndpointResolver[lambda.Options](config))
	input := &lambda.ListFunctionsInput{}

	for {
		functions, err := lambdaClient.ListFunctions(ctx, input)
		if err != nil {
			results.Errorf(err, "failed to list Lambda functions")
			return
		}

		for _, function := range functions.Functions {
			*results = append(*results, v1.ScrapeResult{
				Type:        v1.AWSLambdaFunction,
				ID:          *function.FunctionArn,
				Name:        *function.FunctionName,
				Config:      function,
				ConfigClass: "Lamba",
				BaseScraper: config.BaseScraper,
				Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSLambdaFunction, lo.FromPtr(function.FunctionName), nil)},
				Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
			})
		}

		if functions.NextMarker == nil {
			break
		}
		input.Marker = functions.NextMarker
	}
}

func (aws Scraper) eksClusters(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EKS") {
		return
	}

	ctx.Logger.V(2).Infof("scraping EKS clusters")
	EKS := eks.NewFromConfig(*ctx.Session, getEndpointResolver[eks.Options](config))
	clusters, err := EKS.ListClusters(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to list clusters")
		return
	}

	for _, clusterName := range clusters.Clusters {
		cluster, err := EKS.DescribeCluster(ctx, &eks.DescribeClusterInput{
			Name: strPtr(clusterName),
		})
		if err != nil {
			results.Errorf(err, "failed to describe cluster")
			continue
		}

		var relationships []v1.RelationshipResult
		selfExternalID := v1.ExternalID{ExternalID: lo.FromPtr(cluster.Cluster.Name), ConfigType: v1.AWSEKSCluster}

		// EKS to instance roles relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: selfExternalID,
			ConfigExternalID:  v1.ExternalID{ExternalID: lo.FromPtr(cluster.Cluster.Arn), ConfigType: v1.AWSIAMRole},
			Relationship:      "EKSIAMRole",
		})

		// EKS to subnets relationships
		for _, subnetID := range cluster.Cluster.ResourcesVpcConfig.SubnetIds {
			relationships = append(relationships, v1.RelationshipResult{
				RelatedExternalID: selfExternalID,
				ConfigExternalID:  v1.ExternalID{ExternalID: subnetID, ConfigType: v1.AWSEC2Subnet},
				Relationship:      "SubnetEKS",
			})
		}

		// EKS to security groups relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: selfExternalID,
			ConfigExternalID:  v1.ExternalID{ExternalID: lo.FromPtr(cluster.Cluster.ResourcesVpcConfig.ClusterSecurityGroupId), ConfigType: v1.AWSEC2SecurityGroup},
			Relationship:      "EKSSecuritygroups",
		})

		var parents []v1.ConfigExternalKey
		if vpcID := lo.FromPtr(cluster.Cluster.ResourcesVpcConfig.VpcId); vpcID != "" {
			parents = []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: vpcID}}
		}

		cluster.Cluster.Tags["account"] = lo.FromPtr(ctx.Caller.Account)
		cluster.Cluster.Tags["region"] = getRegionFromArn(*cluster.Cluster.Arn, "eks")

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSEKSCluster,
			CreatedAt:           cluster.Cluster.CreatedAt,
			Status:              string(cluster.Cluster.Status),
			Labels:              cluster.Cluster.Tags,
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEKSCluster, lo.FromPtr(cluster.Cluster.Name), nil)},
			Config:              cluster.Cluster,
			ConfigClass:         "KubernetesCluster",
			Name:                getName(cluster.Cluster.Tags, clusterName),
			Aliases:             []string{*cluster.Cluster.Arn, "AmazonEKS/" + *cluster.Cluster.Arn},
			ID:                  *cluster.Cluster.Arn,
			Ignore:              []string{"createdAt", "name"},
			Parents:             parents,
			RelationshipResults: relationships,
		})

		aws.eksFargateProfiles(ctx, config, EKS, clusterName, results)
	}
}

func (aws Scraper) efs(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EFS") {
		return
	}

	ctx.Logger.V(2).Infof("scraping EFS")
	EFS := efs.NewFromConfig(*ctx.Session, getEndpointResolver[efs.Options](config))
	describeInput := &efs.DescribeFileSystemsInput{}
	describeOutput, err := EFS.DescribeFileSystems(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to get efs")
		return
	}

	for _, fs := range describeOutput.FileSystems {
		labels := make(v1.JSONStringMap)
		for _, tag := range fs.Tags {
			labels[*tag.Key] = *tag.Value
		}

		*results = append(*results, v1.ScrapeResult{
			Type:        "AWS::EFS::FileSystem",
			Labels:      labels,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, "AWS::EFS::FileSystem", lo.FromPtr(fs.FileSystemId), nil)},
			Config:      fs,
			ConfigClass: "FileSystem",
			Name:        getName(labels, *fs.FileSystemId),
			ID:          *fs.FileSystemId,
		})
	}
}

// availabilityZones fetches all the availability zones in the region set in givne the aws session.
func (aws Scraper) availabilityZones(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	ctx.Logger.V(2).Infof("scraping availability zones")

	azDescribeInput := &ec2.DescribeAvailabilityZonesInput{}
	azDescribeOutput, err := ctx.EC2.DescribeAvailabilityZones(ctx, azDescribeInput)
	if err != nil {
		results.Errorf(err, "failed to describe availability zones")
		return
	}

	var uniqueAvailabilityZoneIDs = map[string]struct{}{}
	for _, az := range azDescribeOutput.AvailabilityZones {
		*results = append(*results, v1.ScrapeResult{
			ID:          fmt.Sprintf("%s-%s", *ctx.Caller.Account, lo.FromPtr(az.ZoneName)),
			Type:        v1.AWSAvailabilityZone,
			BaseScraper: config.BaseScraper,
			Config:      az,
			ConfigClass: "AvailabilityZone",
			Tags:        []v1.Tag{{Name: "region", Value: lo.FromPtr(az.RegionName)}},
			Labels:      map[string]string{"az-id": lo.FromPtr(az.ZoneId)},
			Aliases:     []string{lo.FromPtr(az.ZoneName)},
			Name:        lo.FromPtr(az.ZoneName),
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSRegion, ExternalID: lo.FromPtr(az.RegionName)}},
			RelationshipResults: []v1.RelationshipResult{{
				ConfigExternalID:  v1.ExternalID{ExternalID: lo.FromPtr(az.ZoneId), ConfigType: v1.AWSAvailabilityZoneID, ScraperID: "all"},
				RelatedExternalID: v1.ExternalID{ExternalID: lo.FromPtr(az.ZoneName), ConfigType: v1.AWSAvailabilityZone},
			}},
		})

		if _, ok := uniqueAvailabilityZoneIDs[lo.FromPtr(az.ZoneId)]; !ok {
			if az.OptInStatus == "opted-in" {
				az.OptInStatus = "opt-in-required"
			}

			*results = append(*results, v1.ScrapeResult{
				ID:          lo.FromPtr(az.ZoneId),
				Type:        v1.AWSAvailabilityZoneID,
				Tags:        []v1.Tag{{Name: "region", Value: lo.FromPtr(az.RegionName)}},
				BaseScraper: config.BaseScraper,
				Config:      map[string]string{"RegionName": *az.RegionName},
				ConfigClass: "AvailabilityZone",
				Aliases:     nil,
				Name:        lo.FromPtr(az.ZoneId),
				Parents:     []v1.ConfigExternalKey{{Type: v1.AWSRegion, ExternalID: lo.FromPtr(az.RegionName)}},
			})

			uniqueAvailabilityZoneIDs[lo.FromPtr(az.ZoneId)] = struct{}{}
		}
	}
}

func (aws Scraper) account(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Account") {
		return
	}

	ctx.Logger.V(2).Infof("scraping AWS account")

	summary, err := ctx.IAM.GetAccountSummary(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to get account summary")
		return
	}

	aliases, err := ctx.IAM.ListAccountAliases(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to get account aliases")
		return
	}

	name := lo.FromPtr(ctx.Caller.Account)
	if len(aliases.AccountAliases) > 0 {
		name = (*aliases).AccountAliases[0]
	}

	labels := make(map[string]string)
	*results = append(*results, v1.ScrapeResult{
		Type:        v1.AWSAccount,
		BaseScraper: config.BaseScraper,
		Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSAccount, "", nil)},
		Config:      summary.SummaryMap,
		ConfigClass: "Account",
		Name:        name,
		Labels:      labels,
		Aliases:     aliases.AccountAliases,
		ID:          lo.FromPtr(ctx.Caller.Account),
	})

	*results = append(*results, v1.ScrapeResult{
		Type:        v1.AWSIAMUser,
		BaseScraper: config.BaseScraper,
		Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMUser, "root", nil)},
		Config:      summary.SummaryMap,
		Labels:      labels,
		ConfigClass: "User",
		Name:        "root",
		ID:          fmt.Sprintf("%s-%s", lo.FromPtr(ctx.Caller.Account), "root"),
		Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
	})

	regions, err := ctx.EC2.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		results.Errorf(err, "failed to get regions")
		return
	}

	for _, region := range regions.Regions {
		result := v1.ScrapeResult{
			Type:        v1.AWSRegion,
			ConfigClass: "Region",
			BaseScraper: config.BaseScraper,
			Name:        *region.RegionName,
			ID:          *region.RegionName,
		}

		if *region.OptInStatus != "not-opted-in" {
			result.RelationshipResults = []v1.RelationshipResult{
				{
					RelatedExternalID: v1.ExternalID{ConfigType: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)},
					ConfigExternalID:  v1.ExternalID{ConfigType: v1.AWSRegion, ExternalID: *region.RegionName},
				},
			}
		}

		if *region.OptInStatus == "opted-in" || *region.OptInStatus == "not-opted-in" {
			region.OptInStatus = lo.ToPtr("opt-in-required")
		}
		result.Config = region

		*results = append(*results, result)
	}
}

func (aws Scraper) users(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("User") {
		return
	}

	ctx.Logger.V(2).Infof("scraping IAM Users")

	users, err := ctx.IAM.ListUsers(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to get users")
		return
	}

	labels := make(map[string]string)
	for _, user := range users.Users {
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSIAMUser,
			CreatedAt:   user.CreateDate,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMUser, lo.FromPtr(user.UserName), nil)},
			Config:      user,
			ConfigClass: "User",
			Labels:      labels,
			Name:        *user.UserName,
			Aliases:     []string{*user.UserId, *user.UserName},
			Ignore:      []string{"arn", "userId", "createDate", "userName"},
			ID:          *user.Arn, // UserId is not often referenced
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) ebs(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EBS") {
		return
	}

	ctx.Logger.V(2).Infof("scraping EBS")

	describeInput := &ec2.DescribeVolumesInput{}
	describeOutput, err := ctx.EC2.DescribeVolumes(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to get ebs")
		return
	}

	for _, volume := range describeOutput.Volumes {
		labels := getLabels(volume.Tags)

		tags := v1.Tags{}
		tags.Append("zone", *volume.AvailabilityZone)
		tags.Append("region", (*volume.AvailabilityZone)[:len(*volume.AvailabilityZone)-1])

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSEBSVolume,
			Labels:      labels,
			Status:      string(volume.State),
			Tags:        tags,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEBSVolume, lo.FromPtr(volume.VolumeId), nil)},
			Config:      volume,
			ConfigClass: "DiskStorage",
			Aliases:     []string{"AmazonEC2/" + *volume.VolumeId},
			Name:        getName(labels, *volume.VolumeId),
			ID:          *volume.VolumeId,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) rds(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("RDS") {
		return
	}

	ctx.Logger.V(2).Infof("scraping RDS")

	describeInput := &rds.DescribeDBInstancesInput{}
	RDS := rds.NewFromConfig(*ctx.Session, getEndpointResolver[rds.Options](config))
	describeOutput, err := RDS.DescribeDBInstances(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to get rds")
		return
	}

	for _, instance := range describeOutput.DBInstances {
		labels := make(v1.JSONStringMap)
		for _, tag := range instance.TagList {
			labels[*tag.Key] = *tag.Value
		}

		var relationships v1.RelationshipResults
		// SecurityGroup relationships
		for _, sg := range instance.VpcSecurityGroups {
			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID: v1.ExternalID{
					ExternalID: *instance.DBInstanceIdentifier,
					ConfigType: v1.AWSRDSInstance,
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID: *sg.VpcSecurityGroupId,
					ConfigType: v1.AWSEC2SecurityGroup,
				},
				Relationship: "RDSSecurityGroup",
			})
		}

		arn := lo.FromPtr(instance.DBInstanceArn)
		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSRDSInstance,
			Status:              lo.FromPtr(instance.DBInstanceStatus),
			Labels:              labels,
			Tags:                []v1.Tag{{Name: "region", Value: getRegionFromArn(arn, "rds")}},
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSRDSInstance, lo.FromPtr(instance.DBInstanceIdentifier), nil)},
			Config:              instance,
			ConfigClass:         "RelationalDatabase",
			Name:                getName(labels, *instance.DBInstanceIdentifier),
			ID:                  *instance.DBInstanceIdentifier,
			Aliases:             []string{"AmazonRDS/" + arn, arn},
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(instance.DBSubnetGroup.VpcId)}},
			RelationshipResults: relationships,
		})

		// if backups, err := aws.rdsBackups(ctx, config, instance); err != nil {
		// 	results.Errorf(err, "failed to get backups for RDS instances")
		// } else if len(backups) > 0 {
		// 	*results = append(*results, backups...)
		// }
	}

	// Fetch and process restore operations
	if err := aws.rdsEvents(ctx, config, results); err != nil {
		results.Errorf(err, "failed to get restore operations for RDS instances")
	}
}

// func (aws Scraper) rdsBackups(ctx *AWSContext, config v1.AWS, instance rdsTypes.DBInstance) (v1.ScrapeResults, error) {
// 	if !config.Includes("RDSBackup") {
// 		return nil, nil
// 	}

// 	ctx.Logger.V(2).Infof("scraping RDS backups for instance %s", *instance.DBInstanceIdentifier)
// 	var results v1.ScrapeResults

// 	// Get DB snapshots (both automated and manual)
// 	snapshotsInput := &rds.DescribeDBSnapshotsInput{
// 		DBInstanceIdentifier: instance.DBInstanceIdentifier,
// 	}

// 	rdsClient := rds.NewFromConfig(*ctx.Session, getEndpointResolver[rds.Options](config))
// 	snapshots, err := rdsClient.DescribeDBSnapshots(ctx, snapshotsInput)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get RDS snapshots for %s: %w", *instance.DBInstanceIdentifier, err)
// 	}

// 	for _, snapshot := range snapshots.DBSnapshots {
// 		labels := make(v1.JSONStringMap)
// 		for _, tag := range snapshot.TagList {
// 			labels[*tag.Key] = *tag.Value
// 		}

// 		results = append(results, v1.ScrapeResult{
// 			Type:        v1.AWSRDSSnapshot,
// 			Status:      lo.FromPtr(snapshot.Status),
// 			Labels:      labels,
// 			Tags:        []v1.Tag{{Name: "region", Value: getRegionFromArn(*snapshot.DBSnapshotArn, "rds")}},
// 			BaseScraper: config.BaseScraper,
// 			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSRDSSnapshot, lo.FromPtr(snapshot.DBSnapshotIdentifier), nil)},
// 			Config:      snapshot,
// 			ConfigClass: "DatabaseBackup",
// 			Name:        lo.FromPtr(snapshot.DBSnapshotIdentifier),
// 			ID:          lo.FromPtr(snapshot.DBSnapshotArn),
// 			Aliases:     []string{lo.FromPtr(snapshot.DBSnapshotIdentifier)},
// 			CreatedAt:   snapshot.SnapshotCreateTime,
// 			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSRDSInstance, ExternalID: lo.FromPtr(instance.DBInstanceIdentifier)}},
// 			Description: fmt.Sprintf("%s snapshot of %s", lo.FromPtr(snapshot.SnapshotType), lo.FromPtr(instance.DBInstanceIdentifier)),
// 		})
// 	}

// 	return results, nil
// }

func (aws Scraper) rdsEvents(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) error {
	if !config.Includes(IncludeRDSEvents) {
		return nil
	}

	ctx.Logger.V(2).Infof("scraping RDS events")

	// Source: https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_Events.Messages.html#USER_Events.Messages.cluster-snapshot
	sources := []struct {
		Type       rdsTypes.SourceType
		Categories []string
	}{
		{Type: rdsTypes.SourceTypeDbInstance, Categories: []string{"backup", "restoration"}},
		{Type: rdsTypes.SourceTypeDbSnapshot, Categories: []string{"creation", "deletion", "restoration"}},
		{Type: rdsTypes.SourceTypeDbClusterSnapshot, Categories: []string{"backup"}},
	}

	rdsClient := rds.NewFromConfig(*ctx.Session, getEndpointResolver[rds.Options](config))

	var changes []v1.ChangeResult
	for _, source := range sources {
		endTime := time.Now()
		startTime := endTime.Add(-14 * 24 * time.Hour).Add(time.Minute * 5) // 14 days minus 5 minutes to stay just within AWS's limit

		input := &rds.DescribeEventsInput{
			SourceType:      source.Type,
			StartTime:       &startTime,
			EndTime:         &endTime,
			EventCategories: source.Categories,
		}

		paginator := rds.NewDescribeEventsPaginator(rdsClient, input)

		for paginator.HasMorePages() {
			output, err := paginator.NextPage(ctx)
			if err != nil {
				return fmt.Errorf("failed to get RDS events: %w", err)
			}

			for _, event := range output.Events {
				message := lo.FromPtr(event.Message)
				sourceID := lo.FromPtr(event.SourceIdentifier)
				eventID := fmt.Sprintf("%s-%d", sourceID, event.Date.Unix())

				changeType, status, severity := rdsChangeType(source.Type, event.EventCategories[0], message)

				changeResult := v1.ChangeResult{
					ExternalChangeID: eventID,
					ConfigType:       v1.AWSRDSInstance,
					ExternalID:       lo.FromPtr(event.SourceIdentifier),
					ChangeType:       changeType,
					Summary:          message,
					Severity:         string(severity),
					Source:           SourceRDSEvents,
					CreatedAt:        event.Date,
					Details: map[string]any{
						"event":  event,
						"status": lo.PascalCase(status),
					},
				}

				changes = append(changes, changeResult)
			}
		}
	}

	if len(changes) == 0 {
		return nil
	}

	result := v1.NewScrapeResult(config.BaseScraper)
	result.Changes = changes
	*results = append(*results, *result)
	return nil
}

func rdsChangeType(sourceType rdsTypes.SourceType, category, message string) (string, string, models.Severity) {
	switch sourceType {
	case rdsTypes.SourceTypeDbInstance:
		switch category {
		case "backup":
			if strings.Contains(message, "Finished") {
				return "BackupCompleted", "Completed", models.SeverityInfo
			} else if strings.Contains(message, "Backing up") {
				return "BackupStarted", "Started", models.SeverityInfo
			}
			return "BackupCreated", "Completed", models.SeverityInfo
		case "restoration":
			return "BackupRestored", "Completed", models.SeverityMedium
		}

	case rdsTypes.SourceTypeDbSnapshot:
		switch category {
		case "creation":
			if strings.Contains(message, "Creating") {
				return "BackupStarted", "Started", models.SeverityInfo
			} else if strings.Contains(message, "Created") {
				return "BackupCompleted", "Completed", models.SeverityInfo
			}
		case "restoration":
			return "BackupRestored", "Completed", models.SeverityMedium
		case "deletion":
			// TODO: This should delete the original BackupCreated/BackupCompleted changes
			return "BackupDeleted", "Completed", models.SeverityLow
		}
	}

	return "Unknown", "Unknown", models.SeverityInfo
}

func (aws Scraper) vpcs(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("VPC") {
		return
	}

	ctx.Logger.V(2).Infof("scraping VPCs")

	describeInput := &ec2.DescribeVpcsInput{}
	describeOutput, err := ctx.EC2.DescribeVpcs(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to get vpcs")
		return
	}

	for _, vpc := range describeOutput.Vpcs {
		var relationships v1.RelationshipResults
		// DHCPOptions relationship
		relationships = append(relationships, v1.RelationshipResult{
			ConfigExternalID: v1.ExternalID{
				ExternalID: *vpc.VpcId,
				ConfigType: v1.AWSEC2VPC,
			},
			RelatedExternalID: v1.ExternalID{
				ExternalID: *vpc.DhcpOptionsId,
				ConfigType: v1.AWSEC2DHCPOptions,
			},
			Relationship: "VPCDHCPOptions",
		})

		labels := getLabels(vpc.Tags)
		labels["network"] = *vpc.VpcId

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSEC2VPC,
			Status:              string(vpc.State),
			Labels:              labels,
			Tags:                []v1.Tag{{Name: "region", Value: ctx.Session.Region}},
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2VPC, lo.FromPtr(vpc.VpcId), nil)},
			Config:              vpc,
			ConfigClass:         "VPC",
			Name:                getName(labels, *vpc.VpcId),
			ID:                  *vpc.VpcId,
			Aliases:             []string{"AmazonEC2/" + *vpc.VpcId},
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
			RelationshipResults: relationships,
		})
	}
}

func (aws Scraper) instances(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EC2instance") {
		return
	}

	ctx.Logger.V(2).Infof("scraping EC2 instances")

	describeOutput, err := ctx.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	if err != nil {
		results.Errorf(err, "failed to describe instances")
		return
	}

	var relationships v1.RelationshipResults
	for _, r := range describeOutput.Reservations {
		for _, i := range r.Instances {
			selfExternalID := v1.ExternalID{
				ExternalID: *i.InstanceId,
				ConfigType: v1.AWSEC2Instance,
			}

			// SecurityGroup relationships
			for _, sg := range i.SecurityGroups {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID:  v1.ExternalID{ExternalID: *sg.GroupId, ConfigType: v1.AWSEC2SecurityGroup},
					RelatedExternalID: selfExternalID,
					Relationship:      "SecurityGroupInstance",
				})
			}

			// Cluster node relationships
			for _, tag := range i.Tags {
				if *tag.Key == "aws:eks:cluster-name" {
					relationships = append(relationships, v1.RelationshipResult{
						ConfigExternalID:  v1.ExternalID{ExternalID: *tag.Value, ConfigType: v1.AWSEKSCluster},
						RelatedExternalID: selfExternalID,
						Relationship:      "ClusterInstance",
					})
				}
			}

			// Volume relationships
			for _, vol := range i.BlockDeviceMappings {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID:  selfExternalID,
					RelatedExternalID: v1.ExternalID{ExternalID: *vol.Ebs.VolumeId, ConfigType: v1.AWSEBSVolume},
					Relationship:      "EC2InstanceVolume",
				})
			}

			if i.IamInstanceProfile != nil {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID:  v1.ExternalID{ExternalID: *i.IamInstanceProfile.Id, ConfigType: v1.AWSIAMInstanceProfile},
					RelatedExternalID: selfExternalID,
					Relationship:      "IAMInstanceProfileEC2Instance",
				})
			}

			// NOTE: commenting out for now since we aren't currently scraping AMIs
			// relationships = append(relationships, v1.RelationshipResult{
			// 	ConfigExternalID:  v1.ExternalID{ExternalID: []string{*i.ImageId}, ConfigType: v1.AWSEC2AMI},
			// 	RelatedExternalID: selfExternalID,
			// 	Relationship:      "AMIInstance",
			// })

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: lo.FromPtr(i.SubnetId), ConfigType: v1.AWSEC2Subnet},
				RelatedExternalID: selfExternalID,
				Relationship:      "SubnetInstance",
			})

			instance := NewInstance(i)
			labels := instance.Tags
			if labels == nil {
				labels = make(map[string]string)
			}
			labels["network"] = instance.VpcID
			labels["subnet"] = instance.SubnetID

			tags := v1.Tags{}
			zone := ctx.Subnets[instance.SubnetID].Zone
			tags.Append("zone", zone)
			tags.Append("zone-id", ctx.Subnets[instance.SubnetID].ZoneID)
			tags.Append("region", ctx.Subnets[instance.SubnetID].Region)

			*results = append(*results, v1.ScrapeResult{
				Type:                v1.AWSEC2Instance,
				Labels:              labels,
				Status:              string(i.State.Name),
				Tags:                tags,
				BaseScraper:         config.BaseScraper,
				Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2Instance, instance.InstanceID, nil)},
				Config:              instance,
				ConfigClass:         "VirtualMachine",
				Name:                lo.CoalesceOrEmpty(instance.GetHostname(), instance.InstanceID),
				Aliases:             []string{"AmazonEC2/" + instance.InstanceID, fmt.Sprintf("aws:///%s/%s", zone, instance.InstanceID)},
				ID:                  instance.InstanceID,
				Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: instance.VpcID}},
				RelationshipResults: relationships,
			})
		}
	}
}

func (aws Scraper) securityGroups(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("SecurityGroup") {
		return
	}

	ctx.Logger.V(2).Infof("scraping security groups")

	describeInput := &ec2.DescribeSecurityGroupsInput{}
	describeOutput, err := ctx.EC2.DescribeSecurityGroups(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to describe security groups")
		return
	}

	for _, sg := range describeOutput.SecurityGroups {
		labels := getLabels(sg.Tags)
		labels["network"] = *sg.VpcId
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSEC2SecurityGroup,
			Labels:      labels,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2SecurityGroup, lo.FromPtr(sg.GroupId), nil)},
			Config:      sg,
			ConfigClass: "SecurityGroup",
			Name:        getName(labels, *sg.GroupId),
			ID:          *sg.GroupId,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(sg.VpcId)}},
		})
	}
}

func (aws Scraper) routes(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Route") {
		return
	}

	ctx.Logger.V(2).Infof("scraping route tables")

	describeInput := &ec2.DescribeRouteTablesInput{}
	describeOutput, err := ctx.EC2.DescribeRouteTables(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to describe route tables")
		return
	}
	for _, r := range describeOutput.RouteTables {
		labels := getLabels(r.Tags)
		labels["network"] = *r.VpcId

		// Sort associations for a cleaner diff since AWS returns same object in
		// different sort order every time
		slices.SortFunc(r.Associations, func(a, b ec2Types.RouteTableAssociation) int {
			return strings.Compare(lo.FromPtr(a.SubnetId), lo.FromPtr(b.SubnetId))
		})
		*results = append(*results, v1.ScrapeResult{
			Type:        "AWS::EC2::RouteTable",
			Labels:      labels,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, "AWS::EC2::RouteTable", lo.FromPtr(r.RouteTableId), nil)},
			Config:      r,
			ConfigClass: "Route",
			Name:        getName(labels, *r.RouteTableId),
			ID:          *r.RouteTableId,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(r.VpcId)}},
		})
	}
}

func (aws Scraper) dhcp(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("DHCP") {
		return
	}

	ctx.Logger.V(2).Infof("scraping DHCP options")

	describeInput := &ec2.DescribeDhcpOptionsInput{}
	describeOutput, err := ctx.EC2.DescribeDhcpOptions(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to describe dhcp options")
		return
	}

	for _, d := range describeOutput.DhcpOptions {
		labels := getLabels(d.Tags)
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSEC2DHCPOptions,
			Labels:      labels,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2DHCPOptions, lo.FromPtr(d.DhcpOptionsId), nil)},
			Config:      d,
			ConfigClass: "DHCP",
			Name:        getName(labels, *d.DhcpOptionsId),
			ID:          *d.DhcpOptionsId,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) s3Buckets(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("S3Bucket") {
		return
	}

	ctx.Logger.V(2).Infof("scraping S3 buckets")

	S3 := s3.NewFromConfig(*ctx.Session, getEndpointResolver[s3.Options](config))
	buckets, err := S3.ListBuckets(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to list s3 buckets")
		return
	}

	for _, bucket := range buckets.Buckets {
		labels := make(map[string]string)
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSS3Bucket,
			CreatedAt:   bucket.CreationDate,
			BaseScraper: config.BaseScraper,
			Config:      bucket,
			ConfigClass: "ObjectStorage",
			Name:        *bucket.Name,
			Labels:      labels,
			Ignore:      []string{"name", "creationDate"},
			Aliases:     []string{"AmazonS3/" + *bucket.Name, fmt.Sprintf("arn:aws:s3:::%s", *bucket.Name)},
			ID:          *bucket.Name,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSS3Bucket, lo.FromPtr(bucket.Name), nil)},
		})
	}
}

func (aws Scraper) dnsZones(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("DNSZone") {
		return
	}

	ctx.Logger.V(2).Infof("scraping DNS zones")

	Route53 := route53.NewFromConfig(*ctx.Session, getEndpointResolver[route53.Options](config))
	zones, err := Route53.ListHostedZones(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to describe hosted zones")
		return
	}
	for _, zone := range zones.HostedZones {
		var recordSets []map[string]interface{}
		records, err := Route53.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
			HostedZoneId: zone.Id,
		})
		if err != nil {
			results.Errorf(err, "failed to list recordsets")
		} else {

			for _, record := range records.ResourceRecordSets {
				var comments []string
				if record.AliasTarget != nil {
					comments = append(comments, fmt.Sprintf("AliasTarget=%s.%s", lo.FromPtrOr(record.AliasTarget.DNSName, ""),
						lo.FromPtrOr(record.AliasTarget.HostedZoneId, "")))
				}
				if record.Failover != "" {
					comments = append(comments, fmt.Sprintf("Failover=%s", strings.Join(
						lo.Map(record.Failover.Values(), func(item r53types.ResourceRecordSetFailover, _ int) string {
							return string(item)
						}), ",")))
				}
				if record.GeoLocation != nil {
					comments = append(comments, fmt.Sprintf("GeoLocation=%v", record.GeoLocation))
				}
				if record.HealthCheckId != nil {
					comments = append(comments, fmt.Sprintf("HealthCheckId=%s", *record.HealthCheckId))
				}
				if record.MultiValueAnswer != nil {
					comments = append(comments, fmt.Sprintf("MultiValueAnswer=%v", *record.MultiValueAnswer))
				}
				if record.Region != "" {
					comments = append(comments, fmt.Sprintf("Region=%s", strings.Join(
						lo.Map(record.Region.Values(), func(i r53types.ResourceRecordSetRegion, _ int) string {
							return string(i)
						}), ",")))
				}
				if record.SetIdentifier != nil {
					comments = append(comments, fmt.Sprintf("SetIdentifier=%s", *record.SetIdentifier))
				}
				if record.TrafficPolicyInstanceId != nil {
					comments = append(comments, fmt.Sprintf("TrafficPolicyInstanceId=%s", *record.TrafficPolicyInstanceId))
				}
				if record.Weight != nil {
					comments = append(comments, fmt.Sprintf("Weight=%d", *record.Weight))
				}

				recordMap := map[string]interface{}{
					"Name":   *record.Name,
					"Type":   record.Type,
					"Weight": lo.FromPtrOr(record.Weight, 0),
				}

				if record.TTL != nil {
					recordMap["TTL"] = record.TTL
				}

				var values []string
				if record.ResourceRecords != nil {
					for _, rr := range record.ResourceRecords {
						values = append(values, *rr.Value)
					}
				}

				bindRecord := fmt.Sprintf("%s  %d  IN  %s  %s", *record.Name, lo.FromPtrOr(record.TTL, 300), record.Type, strings.Join(values, " "))
				if len(comments) > 0 {
					bindRecord += fmt.Sprintf(" ; %s", strings.Join(comments, ", "))
				}
				recordMap["BindRecord"] = bindRecord
				recordSets = append(recordSets, recordMap)
			}
		}

		// Sort recordSets by weight (descending) then by name and type
		sort.Slice(recordSets, func(i, j int) bool {
			weight1, ok1 := recordSets[i]["Weight"].(int64)
			weight2, ok2 := recordSets[j]["Weight"].(int64)
			if ok1 && ok2 && weight1 != weight2 {
				return weight1 > weight2
			}

			name1, ok1 := recordSets[i]["Name"].(string)
			name2, ok2 := recordSets[j]["Name"].(string)
			if ok1 && ok2 && name1 != name2 {
				return name1 < name2
			}

			type1, ok1 := recordSets[i]["Type"].(string)
			type2, ok2 := recordSets[j]["Type"].(string)
			if ok1 && ok2 {
				return type1 < type2
			}
			return false
		})

		labels := make(map[string]string)
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSZone,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSZone, strings.ReplaceAll(*zone.Id, "/hostedzone/", ""), nil)},
			Config: map[string]interface{}{
				"Id":     *zone.Id,
				"Name":   *zone.Name,
				"Config": zone.Config,
				"ResourceRecordSets": lo.Map(recordSets, func(i map[string]any, _ int) string {
					return i["BindRecord"].(string)
				}),
			},
			ConfigClass: "DNSZone",
			Name:        *zone.Name,
			Labels:      labels,
			Aliases:     []string{*zone.Id, *zone.Name, "AmazonRoute53/arn:aws:route53:::hostedzone/" + *zone.Id},
			ID:          strings.ReplaceAll(*zone.Id, "/hostedzone/", ""),
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) loadBalancers(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("LoadBalancer") {
		return
	}

	ctx.Logger.V(2).Infof("scraping load balancers")

	elb := elasticloadbalancing.NewFromConfig(*ctx.Session, getEndpointResolver[elasticloadbalancing.Options](config))
	loadbalancers, err := elb.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to describe load balancers")
		return
	}

	for _, lb := range loadbalancers.LoadBalancerDescriptions {
		var relationships []v1.RelationshipResult
		for _, instance := range lb.Instances {
			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID: v1.ExternalID{
					ExternalID: *instance.InstanceId,
					ConfigType: v1.AWSEC2Instance,
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID: *lb.LoadBalancerName,
					ConfigType: v1.AWSLoadBalancer,
				},
				Relationship: "LoadBalancerInstance",
			})
		}

		clusterPrefix := "kubernetes.io/cluster/"
		elbTagsOutput, err := elb.DescribeTags(ctx, &elasticloadbalancing.DescribeTagsInput{LoadBalancerNames: []string{*lb.LoadBalancerName}})
		if err != nil {
			logger.Errorf("error while scraping elb tags: %v", err)
			continue
		}
		for _, tagDesc := range elbTagsOutput.TagDescriptions {
			if *tagDesc.LoadBalancerName == *lb.LoadBalancerName {
				for _, tag := range tagDesc.Tags {
					if strings.HasPrefix(*tag.Key, clusterPrefix) {
						clusterName := strings.ReplaceAll(*tag.Key, clusterPrefix, "")
						relationships = append(relationships, v1.RelationshipResult{
							ConfigExternalID: v1.ExternalID{
								ExternalID: *lb.LoadBalancerName,
								ConfigType: v1.AWSLoadBalancer,
							},
							RelatedExternalID: v1.ExternalID{
								ExternalID: clusterName,
								ConfigType: v1.AWSEKSCluster,
							},
							Relationship: "EKSLoadBalancer",
						})
					}
				}
			}
		}

		az := lb.AvailabilityZones[0]
		region := az[:len(az)-1]
		labels := make(map[string]string)
		tags := v1.Tags{}
		tags.Append("zone", az)
		tags.Append("region", region)
		arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", region, lo.FromPtr(ctx.Caller.Account), *lb.LoadBalancerName)
		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSLoadBalancer,
			CreatedAt:           lb.CreatedTime,
			Ignore:              []string{"createdTime"},
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSLoadBalancer, lo.FromPtr(lb.LoadBalancerName), nil)},
			Config:              lb,
			ConfigClass:         "LoadBalancer",
			Name:                *lb.LoadBalancerName,
			Labels:              labels,
			Tags:                tags,
			Aliases:             []string{"AWSELB/" + arn, lo.FromPtr(lb.CanonicalHostedZoneName), *lb.LoadBalancerName},
			ID:                  arn,
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(lb.VPCId)}},
			RelationshipResults: relationships,
		})
	}

	elbv2 := elasticloadbalancingv2.NewFromConfig(*ctx.Session, getEndpointResolver[elasticloadbalancingv2.Options](config))
	loadbalancersv2, err := elbv2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	if err != nil {
		results.Errorf(err, "failed to describe load balancers")
		return
	}

	for _, lb := range loadbalancersv2.LoadBalancers {

		clusterPrefix := "kubernetes.io/cluster/"
		var relationships []v1.RelationshipResult
		elbv2TagsOutput, err := elbv2.DescribeTags(ctx, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: []string{*lb.LoadBalancerArn}})
		if err != nil {
			logger.Errorf("error while scraping elbv2 tags: %v", err)
			continue
		}
		for _, tagDesc := range elbv2TagsOutput.TagDescriptions {
			if *tagDesc.ResourceArn == *lb.LoadBalancerArn {
				for _, tag := range tagDesc.Tags {
					if strings.HasPrefix(*tag.Key, clusterPrefix) {
						clusterName := strings.ReplaceAll(*tag.Key, clusterPrefix, "")
						relationships = append(relationships, v1.RelationshipResult{
							ConfigExternalID: v1.ExternalID{
								ExternalID: *lb.LoadBalancerArn,
								ConfigType: v1.AWSLoadBalancerV2,
							},
							RelatedExternalID: v1.ExternalID{
								ExternalID: clusterName,
								ConfigType: v1.AWSEKSCluster,
							},
							Relationship: "EKSLoadBalancer",
						})
					}
				}
			}
		}
		labels := make(map[string]string)

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSLoadBalancerV2,
			BaseScraper:         config.BaseScraper,
			Status:              string(lb.State.Code),
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSLoadBalancerV2, lo.FromPtr(lb.LoadBalancerArn), nil)},
			Ignore:              []string{"createdTime", "loadBalancerArn", "loadBalancerName"},
			CreatedAt:           lb.CreatedTime,
			Config:              lb,
			ConfigClass:         "LoadBalancer",
			Name:                *lb.LoadBalancerName,
			Aliases:             []string{"AWSELB/" + *lb.LoadBalancerArn},
			ID:                  *lb.LoadBalancerArn,
			Labels:              labels,
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(lb.VpcId)}},
			RelationshipResults: relationships,
		})
	}
}

func (aws Scraper) subnets(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	ctx.Logger.V(2).Infof("scraping subnets")

	// we always need to scrape subnets to get the zone for other resources
	subnets, err := ctx.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{})
	if err != nil {
		results.Errorf(err, "failed to get subnets")
		return
	}

	for _, subnet := range subnets.Subnets {
		// Subnet labels are of the form [{Key: "<key>", Value:
		// "<value>"}, ...]
		labels := make(v1.JSONStringMap)
		for _, tag := range subnet.Tags {
			labels[*tag.Key] = *tag.Value
		}

		az := *subnet.AvailabilityZone
		region := az[0 : len(az)-1]
		ctx.Subnets[*subnet.SubnetId] = Zone{Zone: az, Region: region, ZoneID: *subnet.AvailabilityZoneId}
		labels["network"] = *subnet.VpcId
		labels["subnet"] = *subnet.SubnetId

		tags := v1.Tags{}
		tags.Append("zone", az)
		tags.Append("zone-id", *subnet.AvailabilityZoneId)
		tags.Append("region", region)

		if !config.Includes("subnet") {
			return
		}

		result := v1.ScrapeResult{
			Type:        v1.AWSEC2Subnet,
			Status:      string(subnet.State),
			BaseScraper: config.BaseScraper,
			Labels:      labels,
			Tags:        tags,
			ConfigClass: "Subnet",
			ID:          *subnet.SubnetId,
			Config:      subnet,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(subnet.VpcId)}},
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2Subnet, lo.FromPtr(subnet.SubnetId), nil)},
		}

		*results = append(*results, result)
	}
}

func (aws Scraper) iamRoles(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Roles") {
		return
	}

	roles, err := ctx.IAM.ListRoles(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to get roles")
		return
	}

	for _, role := range roles.Roles {
		labels := make(map[string]string)

		roleMap, err := utils.ToJSONMap(role)
		if err != nil {
			results.Errorf(err, "failed to convert role into json")
			return
		}
		policy, err := parseAssumeRolePolicyDoc(ctx, lo.FromPtr(role.AssumeRolePolicyDocument))
		if err != nil {
			ctx.Errorf("error parsing policy doc[%s]: %v", lo.FromPtr(role.AssumeRolePolicyDocument), err)
		}
		roleMap["AssumeRolePolicyDocument"] = policy

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSIAMRole,
			CreatedAt:   role.CreateDate,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMRole, lo.FromPtr(role.RoleName), nil)},
			Config:      roleMap,
			ConfigClass: "Role",
			Labels:      labels,
			Name:        *role.RoleName,
			Aliases:     []string{*role.RoleName, *role.Arn},
			ID:          *role.RoleId,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

func (aws Scraper) iamProfiles(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Profiles") {
		return
	}

	ctx.Logger.V(2).Infof("scraping IAM profiles")

	profiles, err := ctx.IAM.ListInstanceProfiles(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to get profiles")
		return
	}

	labels := make(map[string]string)
	for _, profile := range profiles.InstanceProfiles {
		// Instance profile to IAM role relationships
		var relationships []v1.RelationshipResult
		for _, role := range profile.Roles {
			relationships = append(relationships, v1.RelationshipResult{
				RelatedExternalID: v1.ExternalID{ExternalID: lo.FromPtr(profile.InstanceProfileId), ConfigType: v1.AWSIAMInstanceProfile},
				ConfigExternalID:  v1.ExternalID{ExternalID: lo.FromPtr(role.Arn), ConfigType: v1.AWSIAMRole},
				Relationship:      "IAMRoleInstanceProfile",
			})
		}

		profileMap, err := utils.ToJSONMap(profile)
		if err != nil {
			results.Errorf(err, "failed to convert profile into json")
			return
		}

		profileObj := gabs.Wrap(profileMap)
		for _, role := range profileObj.S("Roles").Children() {
			policyDocEncoded, ok := role.S("AssumeRolePolicyDocument").Data().(string)
			if !ok {
				ctx.Errorf("AssumeRolePolicyDocument key not found for role: %s", role.String())
				continue
			}
			policy, err := parseAssumeRolePolicyDoc(ctx, policyDocEncoded)
			if err != nil {
				ctx.Errorf("error parsing policy doc[%s]: %v", policyDocEncoded, err)
			}
			if _, err := role.Set(policy, "AssumeRolePolicyDocument"); err != nil {
				ctx.Errorf("error setting policy object[%s] in AssumeRolePolicyDocument: %v", policy, err)
				continue
			}
		}

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSIAMInstanceProfile,
			CreatedAt:           profile.CreateDate,
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMInstanceProfile, lo.FromPtr(profile.Arn), nil)},
			Config:              profileObj.String(),
			Labels:              labels,
			ConfigClass:         "Profile",
			Name:                *profile.InstanceProfileName,
			Aliases:             []string{*profile.InstanceProfileName, *profile.Arn},
			ID:                  *profile.InstanceProfileId,
			RelationshipResults: relationships,
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		})
	}
}

// func (aws Scraper) ami(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
// 	if !config.Includes("Images") {
// 		return
// 	}

// 	amis, err := ctx.EC2.DescribeImages(ctx, &ec2.DescribeImagesInput{})
// 	if err != nil {
// 		results.Errorf(err, "failed to get amis")
// 		return
// 	}

// 	for _, image := range amis.Images {
// 		createdAt, err := time.Parse(time.RFC3339, *image.CreationDate)
// 		if err != nil {
// 			createdAt = time.Now()
// 		}

// 		labels := make(map[string]string)
// 		labels["region"] = lo.FromPtr(ctx.Caller.Account)
// 		*results = append(*results, v1.ScrapeResult{
// 			Type:        v1.AWSEC2AMI,
// 			CreatedAt:   &createdAt,
// 			BaseScraper: config.BaseScraper,
// 			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2AMI, lo.FromPtr(image.ImageId),nil)},
// 			Config:      image,
// 			Labels:      labels,
// 			ConfigClass: "Image",
// 			Name:        ptr.ToString(image.Name),
// 			ID:          *image.ImageId,
// 		})
// 	}
// }

func (aws Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.AWS) > 0
}

func (aws Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	allResults := v1.ScrapeResults{}

	for _, awsConfig := range ctx.ScrapeConfig().Spec.AWS {
		results := &v1.ScrapeResults{}
		var totalResults int

		if len(awsConfig.Regions) == 0 {
			// Use an empty region and the sdk picks the default region
			awsConfig.Regions = []string{""}
		}

		for _, region := range awsConfig.Regions {
			awsCtx, err := aws.getContext(ctx, awsConfig, region)
			if err != nil {
				results.Errorf(err, "failed to create AWS context")
				allResults = append(allResults, *results...)
				continue
			}

			ctx.Logger.V(1).Infof("scraping %s", awsCtx)
			aws.cloudformationStacks(awsCtx, awsConfig, results)
			aws.ecsClusters(awsCtx, awsConfig, results)
			aws.ecsTaskDefinitions(awsCtx, awsConfig, results)
			aws.elastiCache(awsCtx, awsConfig, results)
			aws.lambdaFunctions(awsCtx, awsConfig, results)
			aws.snsTopics(awsCtx, awsConfig, results)
			aws.sqs(awsCtx, awsConfig, results)
			aws.subnets(awsCtx, awsConfig, results)
			aws.instances(awsCtx, awsConfig, results)
			aws.vpcs(awsCtx, awsConfig, results)
			aws.securityGroups(awsCtx, awsConfig, results)
			aws.routes(awsCtx, awsConfig, results)
			aws.dhcp(awsCtx, awsConfig, results)
			aws.eksClusters(awsCtx, awsConfig, results)
			aws.ebs(awsCtx, awsConfig, results)
			aws.efs(awsCtx, awsConfig, results)
			aws.rds(awsCtx, awsConfig, results)
			aws.config(awsCtx, awsConfig, results)
			aws.loadBalancers(awsCtx, awsConfig, results)
			aws.availabilityZones(awsCtx, awsConfig, results)
			aws.containerImages(awsCtx, awsConfig, results)
			aws.cloudtrail(awsCtx, awsConfig, results)
			aws.awsBackups(awsCtx, awsConfig, results)
			// We are querying half a million amis, need to optimize for this
			// aws.ami(awsCtx, awsConfig, results)

			ctx.Logger.V(2).Infof("scraped %d results from region %s", len(*results)-totalResults, region)
		}

		awsCtx, err := aws.getContext(ctx, awsConfig, "us-east-1")
		if err != nil {
			results.Errorf(err, "failed to create AWS context")
			allResults = append(allResults, *results...)
			continue
		}

		aws.account(awsCtx, awsConfig, results)
		aws.users(awsCtx, awsConfig, results)
		aws.iamRoles(awsCtx, awsConfig, results)
		aws.iamProfiles(awsCtx, awsConfig, results)
		aws.dnsZones(awsCtx, awsConfig, results)
		aws.trustedAdvisor(awsCtx, awsConfig, results)
		aws.s3Buckets(awsCtx, awsConfig, results)

		for i, r := range *results {

			rh := health.GetHealthByConfigType(r.Type, r.ConfigMap(), r.Status)

			if rh.Status != "" {
				(*results)[i].Status = string(rh.Status)
			}

			(*results)[i].Health = models.Health(rh.Health)
			(*results)[i].Ready = rh.Ready
			(*results)[i].Description = rh.Message

			if lo.Contains([]string{v1.AWSRegion, v1.AWSAvailabilityZoneID}, r.Type) {
				// We do not need to add tags to these resources.
				// They are global resources.
				continue
			}

			if stack, ok := r.Labels["aws:cloudformation:stack-id"]; ok {
				if len(r.Parents) != 0 {
					// the default parent should be moved to soft relationship
					defaultParent := r.Parents[0]
					(*results)[i].RelationshipResults = append((*results)[i].RelationshipResults, v1.RelationshipResult{
						ConfigExternalID:  v1.ExternalID{ConfigType: defaultParent.Type, ExternalID: defaultParent.ExternalID},
						RelatedExternalID: v1.ExternalID{ConfigType: r.Type, ExternalID: r.ID},
					})
				}

				(*results)[i].Parents = append([]v1.ConfigExternalKey{{
					Type:       v1.AWSCloudFormationStack,
					ExternalID: stack,
				}}, (*results)[i].Parents...)
			}

			(*results)[i].Tags = append((*results)[i].Tags, v1.Tag{
				Name:  "account",
				Value: lo.FromPtr(awsCtx.Caller.Account),
			})

			for _, t := range awsConfig.Tags {
				(*results)[i].Tags = append((*results)[i].Tags, v1.Tag{
					Name:  t.Name,
					Value: t.Value,
				})
			}

			delete((*results)[i].Labels, "name")
			delete((*results)[i].Labels, "Name")
		}

		allResults = append(allResults, *results...)
	}

	return allResults
}

func getConfigTypeById(id string) string {
	prefix := strings.Split(id, "-")[0]
	switch prefix {
	case "i":
		return "AWS::EC2::Instance"
	case "db":
		return "AWS::RDS::DBInstance"
	case "sg":
		return v1.AWSEC2SecurityGroup
	case "vol":
		return "AWS::EBS::Volume"
	case "vpc":
		return "AWS::EC2::VPC"
	case "subnet":
		return v1.AWSEC2Subnet
	}

	return ""
}

func getRegionFromArn(arn, resourceType string) string {
	return strings.Split(strings.ReplaceAll(arn, fmt.Sprintf("arn:aws:%s:", resourceType), ""), ":")[0]
}

func getConsoleLink(region, resourceType, resourceID string, opt map[string]string) *types.Property {
	// resourceID can be a URL in itself - so we encode it.
	resourceID = url.QueryEscape(resourceID)

	var url string
	switch resourceType {
	case v1.AWSCloudFormationStack:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/cloudformation/home?region=%s#/stacks/stackinfo?stackId=%s", region, region, resourceID)
	case v1.AWSEKSFargateProfile:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ecs/home?region=%s#/clusters/%s/fargate-profiles/%s", region, region, resourceID, resourceID)
	case v1.AWSECSTask:
		cluster := opt["cluster"]
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ecs/v2/clusters/%s/tasks/%s/configuration?region=%s", region, cluster, resourceID, region)
	case v1.AWSECSTaskDefinition:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ecs/v2/task-definitions/%s?region=%s", region, resourceID, region)
	case v1.AWSECSService:
		cluster := opt["cluster"]
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ecs/v2/clusters/%s/services/%s?region=%s", region, cluster, resourceID, region)
	case v1.AWSECSCluster:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ecs/v2/clusters/%s/services?region=%s", region, resourceID, region)
	case v1.AWSSQS:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/sqs/v3/home?region=%s#/queues/%s", region, region, resourceID)
	case v1.AWSSNSTopic:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/sns/v3/home?region=%s#/topic/%s", region, region, resourceID)
	case "AWS::ECR::Repository":
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ecr/repositories/%s", region, resourceID)
	case "AWS::EFS::FileSystem":
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/elasticfilesystem/home?region=%s#file-systems:%s", region, region, resourceID)
	case "AWS::EC2::RouteTable":
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/vpcconsole/home?region=%s#RouteTableDetails:RouteTableId=%s", region, region, resourceID)
	case v1.AWSS3Bucket:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/s3/buckets/%s", region, resourceID)
	case v1.AWSEC2Subnet:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/vpcconsole/home?region=%s#SubnetDetails:subnetId=%s", region, region, resourceID)
	case v1.AWSLambdaFunction:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/lambda/home?region=%s#/functions/%s", region, region, resourceID)
	case v1.AWSEKSCluster:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/eks/home?region=%s#/clusters/%s", region, region, resourceID)
	case v1.AWSLoadBalancer:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ec2/home?region=%s#LoadBalancer:loadBalancerArn=%s;tab=listeners", region, region, resourceID)
	case v1.AWSLoadBalancerV2:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ec2/v2/home?region=%s#LoadBalancersV2:search=%s", region, region, resourceID)
	case v1.AWSEBSVolume:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ec2/v2/home?region=%s#VolumeDetails:volumeId=%s", region, region, resourceID)
	case v1.AWSRDSInstance:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/rds/home?region=%s#database:id=%s", region, region, resourceID)
	case v1.AWSEC2VPC:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/vpc/home?region=%s#VpcDetails:VpcId=%s", region, region, resourceID)
	case v1.AWSAccount:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/console/home?region=%s", region, region)
	case v1.AWSEC2SecurityGroup:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ec2/v2/home?region=%s#SecurityGroups:group-id=%s", region, region, resourceID)
	case v1.AWSIAMUser:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/iam/home?region=%s#users/%s", region, region, resourceID)
	case v1.AWSIAMRole:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/iam/home?region=%s#roles/details/%s", region, region, resourceID)
	case v1.AWSIAMInstanceProfile:
		url = fmt.Sprintf("https://console.aws.amazon.com/go/view?arn=%s", resourceID)
	case v1.AWSEC2AMI:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ec2/v2/home?region=%s#AMIDetails:amisearch=%s", region, region, resourceID)
	case v1.AWSEC2DHCPOptions:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/vpcconsole/home?region=%s#DhcpOptionsDetails:DhcpOptionsId=%s", region, region, resourceID)
	case v1.AWSZone:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/route53/v2/hostedzones?region=%s#ListRecordSets/%s", region, region, resourceID)

	case v1.AWSRegion, v1.AWSAvailabilityZone, v1.AWSAvailabilityZoneID:
		// Not applicable
		return nil
	}

	return &types.Property{
		Name: "URL",
		Icon: resourceType,
		Links: []types.Link{
			{
				Text: types.Text{Label: "Console"},
				URL:  url,
			},
		},
	}
}

func formatStatus(status string) string {
	return lo.Capitalize(strings.ReplaceAll(strings.ReplaceAll(status, "-", " "), "_", " "))
}

// unwrapFields unwraps the given JSON encoded fields in the map.
func unwrapFields(m map[string]string, fields ...string) map[string]any {
	var output = make(map[string]any)
	for k, v := range m {
		if lo.Contains(fields, k) {
			var unwrapped map[string]any
			if err := json.Unmarshal([]byte(v), &unwrapped); err != nil {
				output[k] = v
			} else {
				output[k] = unwrapped
			}
		} else {
			output[k] = v
		}
	}

	return output
}

func parseAssumeRolePolicyDoc(ctx *AWSContext, encodedDoc string) (map[string]any, error) {
	doc, err := url.QueryUnescape(encodedDoc)
	if err != nil {
		return nil, fmt.Errorf("error escaping policy doc[%s]: %v", encodedDoc, err)
	}

	var policyDocObj map[string]any
	if err := json.Unmarshal([]byte(doc), &policyDocObj); err != nil {
		return nil, fmt.Errorf("error escaping policy doc[%s]: %v", doc, err)
	}

	c := gabs.Wrap(policyDocObj)
	for _, stmt := range c.S("Statement").Children() {
		// If Principal.Service is a list, sort that for cleaner change diff
		svcsObj := stmt.Search("Principal", "Service").Data()
		if svcsObj == nil {
			continue
		}
		if svcAnySlice, ok := svcsObj.([]any); ok {
			if svcs, ok := lo.FromAnySlice[string](svcAnySlice); ok {
				slices.Sort(svcs)
				if _, err := stmt.Set(svcs, "Principal", "Service"); err != nil {
					ctx.Errorf("error setting services object[%v] in Principal.Services: %v", svcs, err)
					continue
				}
			}
		}
	}
	var policyDoc map[string]any
	err = json.Unmarshal(c.Bytes(), &policyDoc)
	return policyDoc, err
}
