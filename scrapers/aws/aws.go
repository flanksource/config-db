package aws

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
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
	"github.com/aws/aws-sdk-go-v2/service/route53"
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

func (aws Scraper) getContext(ctx api.ScrapeContext, awsConfig v1.AWS, region string) (*AWSContext, error) {
	session, err := NewSession(ctx, awsConfig.AWSConnection, region)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session for region=%q: %w", region, err)
	}

	STS := sts.NewFromConfig(*session)
	caller, err := STS.GetCallerIdentity(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get identity for region=%q: %w", region, err)
	}

	usEast1 := session.Copy()
	usEast1.Region = "us-east-1"

	return &AWSContext{
		ScrapeContext: ctx,
		Session:       session,
		Caller:        caller,
		STS:           STS,
		Support:       support.NewFromConfig(usEast1),
		EC2:           ec2.NewFromConfig(*session),
		SSM:           ssm.NewFromConfig(*session),
		IAM:           iam.NewFromConfig(*session),
		Subnets:       make(map[string]Zone),
		Config:        configservice.NewFromConfig(*session),
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
	Region, Zone string
}

func (aws Scraper) containerImages(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("ECR") {
		return
	}

	ctx.Logger.V(2).Infof("scraping ECR")

	ECR := ecr.NewFromConfig(*ctx.Session)
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

	client := sqs.NewFromConfig(*ctx.Session)
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

	client := cloudformation.NewFromConfig(*ctx.Session)
	stacks, err := client.ListStacks(ctx, &cloudformation.ListStacksInput{})
	if err != nil {
		results.Errorf(err, "failed to list CloudFormation stacks")
		return
	}

	for _, stack := range stacks.StackSummaries {
		stackName := lo.FromPtr(stack.StackName)

		resourceHealth := health.GetAWSResourceHealth(health.AWSResourceTypeCloudformationStack, string(stack.StackStatus))
		resourceHealth.Message = lo.FromPtr(stack.StackStatusReason)

		*results = append(*results, v1.ScrapeResult{
			Type:         v1.AWSCloudFormationStack,
			BaseScraper:  config.BaseScraper,
			Properties:   []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSCloudFormationStack, stackName, nil)},
			Config:       stack,
			CreatedAt:    stack.CreationTime,
			DeletedAt:    stack.DeletionTime,
			DeleteReason: v1.DeletedReasonFromAttribute,
			ConfigClass:  "Stack",
			Name:         stackName,
			ID:           *stack.StackId,
			Aliases:      []string{lo.FromPtr(stack.StackId)},
			Parents:      []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		}.WithHealthStatus(resourceHealth))
	}
}

func (aws Scraper) snsTopics(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("sns") {
		return
	}

	ctx.Logger.V(2).Infof("scraping SNS topics")

	client := sns.NewFromConfig(*ctx.Session)
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

	client := ecs.NewFromConfig(*ctx.Session)
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
			RelatedExternalID: v1.ExternalID{ExternalID: []string{*service.ServiceArn}, ConfigType: v1.AWSECSService},
			ConfigExternalID:  v1.ExternalID{ExternalID: []string{*service.TaskDefinition}, ConfigType: v1.AWSECSTaskDefinition},
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
						ConfigExternalID:  v1.ExternalID{ExternalID: []string{*task.TaskDefinitionArn}, ConfigType: v1.AWSECSTaskDefinition},
						RelatedExternalID: v1.ExternalID{ExternalID: []string{*task.TaskArn}, ConfigType: v1.AWSECSTask},
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

	client := ecs.NewFromConfig(*ctx.Session)
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

	svc := elasticache.NewFromConfig(*ctx.Session)

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

	lambdaClient := lambda.NewFromConfig(*ctx.Session)
	input := &lambda.ListFunctionsInput{}

	for {
		functions, err := lambdaClient.ListFunctions(ctx, input)
		if err != nil {
			results.Errorf(err, "failed to list Lambda functions")
			return
		}

		var resourceHealth health.HealthStatus // TODO: Lambda health check
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
			}.WithHealthStatus(resourceHealth))
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

	EKS := eks.NewFromConfig(*ctx.Session)
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
		selfExternalID := v1.ExternalID{ExternalID: []string{lo.FromPtr(cluster.Cluster.Name)}, ConfigType: v1.AWSEKSCluster}

		// EKS to instance roles relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: selfExternalID,
			ConfigExternalID:  v1.ExternalID{ExternalID: []string{lo.FromPtr(cluster.Cluster.Arn)}, ConfigType: v1.AWSIAMRole},
			Relationship:      "EKSIAMRole",
		})

		// EKS to subnets relationships
		for _, subnetID := range cluster.Cluster.ResourcesVpcConfig.SubnetIds {
			relationships = append(relationships, v1.RelationshipResult{
				RelatedExternalID: selfExternalID,
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{subnetID}, ConfigType: v1.AWSEC2Subnet},
				Relationship:      "SubnetEKS",
			})
		}

		// EKS to security groups relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: selfExternalID,
			ConfigExternalID:  v1.ExternalID{ExternalID: []string{lo.FromPtr(cluster.Cluster.ResourcesVpcConfig.ClusterSecurityGroupId)}, ConfigType: v1.AWSEC2SecurityGroup},
			Relationship:      "EKSSecuritygroups",
		})

		resourceHealth := health.GetAWSResourceHealth(health.AWSResourceTypeEKS, string(cluster.Cluster.Status))

		cluster.Cluster.Tags["account"] = lo.FromPtr(ctx.Caller.Account)
		cluster.Cluster.Tags["region"] = getRegionFromArn(*cluster.Cluster.Arn, "eks")

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSEKSCluster,
			CreatedAt:           cluster.Cluster.CreatedAt,
			Labels:              cluster.Cluster.Tags,
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEKSCluster, lo.FromPtr(cluster.Cluster.Name), nil)},
			Config:              cluster.Cluster,
			ConfigClass:         "KubernetesCluster",
			Name:                getName(cluster.Cluster.Tags, clusterName),
			Aliases:             []string{*cluster.Cluster.Arn, "AmazonEKS/" + *cluster.Cluster.Arn},
			ID:                  *cluster.Cluster.Arn,
			Ignore:              []string{"createdAt", "name"},
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: *cluster.Cluster.ResourcesVpcConfig.VpcId}},
			RelationshipResults: relationships,
		}.WithHealthStatus(resourceHealth))

		aws.eksFargateProfiles(ctx, config, EKS, clusterName, results)
	}
}

func (aws Scraper) efs(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EFS") {
		return
	}

	ctx.Logger.V(2).Infof("scraping EFS")

	describeInput := &efs.DescribeFileSystemsInput{}
	EFS := efs.NewFromConfig(*ctx.Session)
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
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{lo.FromPtr(az.ZoneId)}, ConfigType: v1.AWSAvailabilityZoneID, ScraperID: "all"},
				RelatedExternalID: v1.ExternalID{ExternalID: []string{lo.FromPtr(az.ZoneName)}, ConfigType: v1.AWSAvailabilityZone},
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
				ScraperLess: true,
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
			ScraperLess: true,
		}

		if *region.OptInStatus != "not-opted-in" {
			result.RelationshipResults = []v1.RelationshipResult{
				{
					RelatedExternalID: v1.ExternalID{ConfigType: v1.AWSAccount, ExternalID: []string{lo.FromPtr(ctx.Caller.Account)}},
					ConfigExternalID:  v1.ExternalID{ConfigType: v1.AWSRegion, ExternalID: []string{*region.RegionName}},
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
			Aliases:     []string{*user.UserId, *user.Arn},
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

		resourceHealth := health.GetAWSResourceHealth(health.AWSResourceTypeEBS, string(volume.State))

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSEBSVolume,
			Labels:      labels,
			Tags:        tags,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEBSVolume, lo.FromPtr(volume.VolumeId), nil)},
			Config:      volume,
			ConfigClass: "DiskStorage",
			Aliases:     []string{"AmazonEC2/" + *volume.VolumeId},
			Name:        getName(labels, *volume.VolumeId),
			ID:          *volume.VolumeId,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
		}.WithHealthStatus(resourceHealth))
	}
}

func (aws Scraper) rds(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("RDS") {
		return
	}

	ctx.Logger.V(2).Infof("scraping RDS")

	describeInput := &rds.DescribeDBInstancesInput{}
	RDS := rds.NewFromConfig(*ctx.Session)
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
					ExternalID: []string{*instance.DBInstanceIdentifier},
					ConfigType: v1.AWSRDSInstance,
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID: []string{*sg.VpcSecurityGroupId},
					ConfigType: v1.AWSEC2SecurityGroup,
				},
				Relationship: "RDSSecurityGroup",
			})
		}

		resourceHealth := health.GetAWSResourceHealth(health.AWSResourceTypeRDS, lo.FromPtr(instance.DBInstanceStatus))

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSRDSInstance,
			Labels:              labels,
			Tags:                []v1.Tag{{Name: "region", Value: getRegionFromArn(*instance.DBInstanceArn, "rds")}},
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSRDSInstance, lo.FromPtr(instance.DBInstanceIdentifier), nil)},
			Config:              instance,
			ConfigClass:         "RelationalDatabase",
			Name:                getName(labels, *instance.DBInstanceIdentifier),
			ID:                  *instance.DBInstanceIdentifier,
			Aliases:             []string{"AmazonRDS/" + *instance.DBInstanceArn},
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(instance.DBSubnetGroup.VpcId)}},
			RelationshipResults: relationships,
		}.WithHealthStatus(resourceHealth))
	}
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
				ExternalID: []string{*vpc.VpcId},
				ConfigType: v1.AWSEC2VPC,
			},
			RelatedExternalID: v1.ExternalID{
				ExternalID: []string{*vpc.DhcpOptionsId},
				ConfigType: v1.AWSEC2DHCPOptions,
			},
			Relationship: "VPCDHCPOptions",
		})

		// VPC to region relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: v1.ExternalID{ExternalID: []string{string(*vpc.VpcId)}, ConfigType: v1.AWSEC2VPC},
			ConfigExternalID:  v1.ExternalID{ExternalID: []string{ctx.Session.Region}, ConfigType: v1.AWSRegion},
			Relationship:      "RegionVPC",
		})

		labels := getLabels(vpc.Tags)
		labels["network"] = *vpc.VpcId

		resourceHealth := health.GetAWSResourceHealth(health.AWSResourceTypeVPC, string(vpc.State))
		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSEC2VPC,
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
		}.WithHealthStatus(resourceHealth))
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
				ExternalID: []string{*i.InstanceId},
				ConfigType: v1.AWSEC2Instance,
			}

			// SecurityGroup relationships
			for _, sg := range i.SecurityGroups {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID:  v1.ExternalID{ExternalID: []string{*sg.GroupId}, ConfigType: v1.AWSEC2SecurityGroup},
					RelatedExternalID: selfExternalID,
					Relationship:      "SecurityGroupInstance",
				})
			}

			// Cluster node relationships
			for _, tag := range i.Tags {
				if *tag.Key == "aws:eks:cluster-name" {
					relationships = append(relationships, v1.RelationshipResult{
						ConfigExternalID:  v1.ExternalID{ExternalID: []string{*tag.Value}, ConfigType: v1.AWSEKSCluster},
						RelatedExternalID: selfExternalID,
						Relationship:      "ClusterInstance",
					})
				}
			}

			// Volume relationships
			for _, vol := range i.BlockDeviceMappings {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID:  selfExternalID,
					RelatedExternalID: v1.ExternalID{ExternalID: []string{*vol.Ebs.VolumeId}, ConfigType: v1.AWSEBSVolume},
					Relationship:      "EC2InstanceVolume",
				})
			}

			if i.IamInstanceProfile != nil {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID:  v1.ExternalID{ExternalID: []string{*i.IamInstanceProfile.Id}, ConfigType: v1.AWSIAMInstanceProfile},
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
				ConfigExternalID:  selfExternalID,
				RelatedExternalID: v1.ExternalID{ExternalID: []string{"Kubernetes/Node//" + *i.PrivateDnsName}, ConfigType: "Kubernetes::Node"},
				Relationship:      "InstanceKuberenetesNode",
			})

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{lo.FromPtr(i.SubnetId)}, ConfigType: v1.AWSEC2Subnet},
				RelatedExternalID: selfExternalID,
				Relationship:      "SubnetInstance",
			})

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{ctx.Session.Region}, ConfigType: v1.AWSRegion},
				RelatedExternalID: selfExternalID,
				Relationship:      "RegionInstance",
			})

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{ctx.Subnets[lo.FromPtr(i.SubnetId)].Zone}, ConfigType: v1.AWSAvailabilityZone},
				RelatedExternalID: selfExternalID,
				Relationship:      "ZoneInstance",
			})

			instance := NewInstance(i)
			labels := instance.Tags
			if labels == nil {
				labels = make(map[string]string)
			}
			labels["network"] = instance.VpcID
			labels["subnet"] = instance.SubnetID

			tags := v1.Tags{}
			tags.Append("zone", ctx.Subnets[instance.SubnetID].Zone)
			tags.Append("region", ctx.Subnets[instance.SubnetID].Region)

			resourceHealth := health.GetAWSResourceHealth(health.AWSResourceTypeEC2, string(i.State.Name))

			*results = append(*results, v1.ScrapeResult{
				Type:                v1.AWSEC2Instance,
				Labels:              labels,
				BaseScraper:         config.BaseScraper,
				Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2Instance, instance.InstanceID, nil)},
				Config:              instance,
				ConfigClass:         "VirtualMachine",
				Name:                instance.GetHostname(),
				Aliases:             []string{"AmazonEC2/" + instance.InstanceID},
				ID:                  instance.InstanceID,
				Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: instance.VpcID}},
				RelationshipResults: relationships,
			}.WithHealthStatus(resourceHealth))
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

	S3 := s3.NewFromConfig(*ctx.Session)
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
			Aliases:     []string{"AmazonS3/" + *bucket.Name},
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

	Route53 := route53.NewFromConfig(*ctx.Session)
	zones, err := Route53.ListHostedZones(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to describe hosted zones")
		return
	}
	for _, zone := range zones.HostedZones {
		labels := make(map[string]string)
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSZone,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSZone, strings.ReplaceAll(*zone.Id, "/hostedzone/", ""), nil)},
			Config:      zone,
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

	elb := elasticloadbalancing.NewFromConfig(*ctx.Session)
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
					ExternalID: []string{*instance.InstanceId},
					ConfigType: v1.AWSEC2Instance,
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID: []string{*lb.LoadBalancerName},
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
								ExternalID: []string{*lb.LoadBalancerName},
								ConfigType: v1.AWSLoadBalancer,
							},
							RelatedExternalID: v1.ExternalID{
								ExternalID: []string{clusterName},
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
			Aliases:             []string{"AWSELB/" + arn, lo.FromPtr(lb.CanonicalHostedZoneName)},
			ID:                  arn,
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(lb.VPCId)}},
			RelationshipResults: relationships,
		})
	}

	elbv2 := elasticloadbalancingv2.NewFromConfig(*ctx.Session)
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
								ExternalID: []string{*lb.LoadBalancerArn},
								ConfigType: v1.AWSLoadBalancerV2,
							},
							RelatedExternalID: v1.ExternalID{
								ExternalID: []string{clusterName},
								ConfigType: v1.AWSEKSCluster,
							},
							Relationship: "EKSLoadBalancer",
						})
					}
				}
			}
		}
		labels := make(map[string]string)

		resourceHealth := health.GetAWSResourceHealth(health.AWSResourceTypeELB, string(lb.State.Code))

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSLoadBalancerV2,
			BaseScraper:         config.BaseScraper,
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
		}.WithHealthStatus(resourceHealth))
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
		ctx.Subnets[*subnet.SubnetId] = Zone{Zone: az, Region: region}
		labels["network"] = *subnet.VpcId
		labels["subnet"] = *subnet.SubnetId

		tags := v1.Tags{}
		tags.Append("zone", az)
		tags.Append("region", region)

		if !config.Includes("subnet") {
			return
		}

		var relationships []v1.RelationshipResult
		selfExternalID := v1.ExternalID{ExternalID: []string{lo.FromPtr(subnet.SubnetId)}, ConfigType: v1.AWSEC2Subnet}

		// Subnet to Region relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: selfExternalID,
			ConfigExternalID:  v1.ExternalID{ExternalID: []string{ctx.Session.Region}, ConfigType: v1.AWSRegion},
			Relationship:      "RegionSubnet",
		})

		// Subnet to availability zone relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: selfExternalID,
			ConfigExternalID:  v1.ExternalID{ExternalID: []string{lo.FromPtr(subnet.AvailabilityZone)}, ConfigType: v1.AWSAvailabilityZone},
			Relationship:      "AvailabilityZoneSubnet",
		})

		// Subnet to availability zone relationship
		relationships = append(relationships, v1.RelationshipResult{
			RelatedExternalID: selfExternalID,
			ConfigExternalID:  v1.ExternalID{ExternalID: []string{lo.FromPtr(subnet.AvailabilityZoneId)}, ConfigType: v1.AWSAvailabilityZoneID},
			Relationship:      "AvailabilityZoneIDSubnet",
		})

		resourceHealth := health.GetAWSResourceHealth(health.AWSResourceTypeSubnet, string(subnet.State))

		result := v1.ScrapeResult{
			Type:                v1.AWSEC2Subnet,
			BaseScraper:         config.BaseScraper,
			Labels:              labels,
			Tags:                tags,
			ConfigClass:         "Subnet",
			ID:                  *subnet.SubnetId,
			Config:              subnet,
			Parents:             []v1.ConfigExternalKey{{Type: v1.AWSEC2VPC, ExternalID: lo.FromPtr(subnet.VpcId)}},
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2Subnet, lo.FromPtr(subnet.SubnetId), nil)},
			RelationshipResults: relationships,
		}.WithHealthStatus(resourceHealth)

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

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSIAMRole,
			CreatedAt:   role.CreateDate,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMRole, lo.FromPtr(role.RoleName), nil)},
			Config:      role,
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
				RelatedExternalID: v1.ExternalID{ExternalID: []string{lo.FromPtr(profile.InstanceProfileId)}, ConfigType: v1.AWSIAMInstanceProfile},
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{lo.FromPtr(role.Arn)}, ConfigType: v1.AWSIAMRole},
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
				logger.Errorf("AssumeRolePolicyDocument key not found for role: %s", role.String())
				continue
			}
			doc, err := url.QueryUnescape(policyDocEncoded)
			if err != nil {
				logger.Errorf("error escaping policy doc[%s]: %v", policyDocEncoded, err)
				continue
			}
			policyDocObj, err := gabs.ParseJSON([]byte(doc))
			if err != nil {
				logger.Errorf("error escaping policy doc[%s]: %v", policyDocEncoded, err)
				continue
			}
			if _, err := role.Set(policyDocObj, "AssumeRolePolicyDocument"); err != nil {
				logger.Errorf("error setting policy object[%s] in AssumeRolePolicyDocument: %v", policyDocObj.String(), err)
				continue
			}
			for _, stmt := range policyDocObj.S("Statement").Children() {
				// If Principal.Service is a list, sort that for cleaner change diff
				if svcs, ok := stmt.Search("Principal", "Service").Data().([]string); ok {
					slices.Sort(svcs)
					if _, err := stmt.Set(svcs, "Principal", "Service"); err != nil {
						logger.Errorf("error setting services object[%v] in Principal.Services: %v", svcs, err)
						continue
					}
				}
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
		for _, region := range awsConfig.Region {
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
						ConfigExternalID:  v1.ExternalID{ConfigType: defaultParent.Type, ExternalID: []string{defaultParent.ExternalID}},
						RelatedExternalID: v1.ExternalID{ConfigType: r.Type, ExternalID: []string{r.ID}},
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
	case v1.AWSEC2Instance:
		url = fmt.Sprintf("https://%s.console.aws.amazon.com/ec2/v2/home?region=%s#Instances:search=%s", region, region, resourceID)
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
