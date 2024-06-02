package aws

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/ptr"
	"github.com/samber/lo"

	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	"github.com/aws/aws-sdk-go-v2/service/support"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/is-healthy/pkg/health"
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
	return fmt.Sprintf("account=%s user=%s region=%s", *ctx.Caller.Account, *ctx.Caller.UserId, ctx.Session.Region)
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
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, "AWS::ECR::Repository", lo.FromPtr(image.RepositoryName))},
			Config:      image,
			Labels:      labels,
			ConfigClass: "ContainerRegistry",
			Name:        *image.RepositoryName,
			Aliases:     []string{*image.RepositoryArn, "AmazonECR/" + *image.RepositoryArn},
			ID:          *image.RepositoryUri,
			Ignore: []string{
				"CreatedAt", "RepositoryArn", "RepositoryUri", "RegistryId", "RepositoryName",
			},
			ParentExternalID: *ctx.Caller.Account,
			ParentType:       v1.AWSAccount,
		})
	}
}

func (aws Scraper) eksClusters(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EKS") {
		return
	}

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

		cluster.Cluster.Tags["account"] = *ctx.Caller.Account
		cluster.Cluster.Tags["region"] = getRegionFromArn(*cluster.Cluster.Arn, "eks")

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSEKSCluster,
			CreatedAt:           cluster.Cluster.CreatedAt,
			Labels:              cluster.Cluster.Tags,
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEKSCluster, lo.FromPtr(cluster.Cluster.Name))},
			Config:              cluster.Cluster,
			ConfigClass:         "KubernetesCluster",
			Name:                getName(cluster.Cluster.Tags, clusterName),
			Aliases:             []string{*cluster.Cluster.Arn, "AmazonEKS/" + *cluster.Cluster.Arn},
			ID:                  *cluster.Cluster.Name,
			Ignore:              []string{"createdAt", "name"},
			ParentExternalID:    *cluster.Cluster.ResourcesVpcConfig.VpcId,
			ParentType:          v1.AWSEC2VPC,
			RelationshipResults: relationships,
		}.WithHealthStatus(resourceHealth))
	}
}

func (aws Scraper) efs(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EFS") {
		return
	}

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
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, "AWS::EFS::FileSystem", lo.FromPtr(fs.FileSystemId))},
			Config:      fs,
			ConfigClass: "FileSystem",
			Name:        getName(labels, *fs.FileSystemId),
			ID:          *fs.FileSystemId,
		})
	}
}

// availabilityZones fetches all the availability zones in the region set in givne the aws session.
func (aws Scraper) availabilityZones(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	azDescribeInput := &ec2.DescribeAvailabilityZonesInput{}
	azDescribeOutput, err := ctx.EC2.DescribeAvailabilityZones(ctx, azDescribeInput)
	if err != nil {
		results.Errorf(err, "failed to describe availability zones")
		return
	}

	var uniqueAvailabilityZoneIDs = map[string]struct{}{}
	for _, az := range azDescribeOutput.AvailabilityZones {
		*results = append(*results, v1.ScrapeResult{
			ID:               lo.FromPtr(az.ZoneName),
			Type:             v1.AWSAvailabilityZone,
			BaseScraper:      config.BaseScraper,
			Config:           az,
			ConfigClass:      "AvailabilityZone",
			Tags:             []v1.Tag{{Name: "region", Value: lo.FromPtr(az.RegionName)}},
			Aliases:          nil,
			Name:             lo.FromPtr(az.ZoneName),
			ParentExternalID: lo.FromPtr(az.RegionName),
			ParentType:       v1.AWSRegion,
		})

		if _, ok := uniqueAvailabilityZoneIDs[lo.FromPtr(az.ZoneId)]; !ok {
			*results = append(*results, v1.ScrapeResult{
				ID:               lo.FromPtr(az.ZoneId),
				Type:             v1.AWSAvailabilityZoneID,
				Tags:             []v1.Tag{{Name: "region", Value: lo.FromPtr(az.RegionName)}},
				BaseScraper:      config.BaseScraper,
				Config:           map[string]string{"RegionName": *az.RegionName},
				ConfigClass:      "AvailabilityZone",
				Aliases:          nil,
				Name:             lo.FromPtr(az.ZoneId),
				ParentExternalID: lo.FromPtr(az.RegionName),
				ParentType:       v1.AWSRegion,
			})

			uniqueAvailabilityZoneIDs[lo.FromPtr(az.ZoneId)] = struct{}{}
		}
	}
}

func (aws Scraper) account(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Account") {
		return
	}

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

	name := *ctx.Caller.Account
	if len(aliases.AccountAliases) > 0 {
		name = (*aliases).AccountAliases[0]
	}

	labels := make(map[string]string)
	*results = append(*results, v1.ScrapeResult{
		Type:        v1.AWSAccount,
		BaseScraper: config.BaseScraper,
		Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSAccount, "")},
		Config:      summary.SummaryMap,
		ConfigClass: "Account",
		Name:        name,
		Labels:      labels,
		Aliases:     aliases.AccountAliases,
		ID:          *ctx.Caller.Account,
	})

	*results = append(*results, v1.ScrapeResult{
		Type:             v1.AWSIAMUser,
		BaseScraper:      config.BaseScraper,
		Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMUser, "root")},
		Config:           summary.SummaryMap,
		Labels:           labels,
		ConfigClass:      "User",
		Name:             "root",
		Aliases:          []string{"<root account>"},
		ID:               "root",
		ParentExternalID: lo.FromPtr(ctx.Caller.Account),
		ParentType:       v1.AWSAccount,
	})

	regions, err := ctx.EC2.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		results.Errorf(err, "failed to get regions")
		return
	}

	for _, region := range regions.Regions {
		if *region.OptInStatus == "not-opted-in" {
			continue
		}

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSRegion,
			ConfigClass: "Region",
			BaseScraper: config.BaseScraper,
			Config:      region,
			Name:        *region.RegionName,
			Labels:      labels,
			ID:          *region.RegionName,
		})
	}
}

func (aws Scraper) users(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("User") {
		return
	}
	users, err := ctx.IAM.ListUsers(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to get users")
		return
	}

	labels := make(map[string]string)
	for _, user := range users.Users {
		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSIAMUser,
			CreatedAt:        user.CreateDate,
			BaseScraper:      config.BaseScraper,
			Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMUser, lo.FromPtr(user.UserName))},
			Config:           user,
			ConfigClass:      "User",
			Labels:           labels,
			Name:             *user.UserName,
			Aliases:          []string{*user.UserId, *user.Arn},
			Ignore:           []string{"arn", "userId", "createDate", "userName"},
			ID:               *user.UserName, // UserId is not often referenced
			ParentExternalID: lo.FromPtr(ctx.Caller.Account),
			ParentType:       v1.AWSAccount,
		})
	}
}

func (aws Scraper) ebs(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EBS") {
		return
	}

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
			Type:             v1.AWSEBSVolume,
			Labels:           labels,
			Tags:             tags,
			BaseScraper:      config.BaseScraper,
			Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEBSVolume, lo.FromPtr(volume.VolumeId))},
			Config:           volume,
			ConfigClass:      "DiskStorage",
			Aliases:          []string{"AmazonEC2/" + *volume.VolumeId},
			Name:             getName(labels, *volume.VolumeId),
			ID:               *volume.VolumeId,
			ParentExternalID: lo.FromPtr(ctx.Caller.Account),
			ParentType:       v1.AWSAccount,
		}.WithHealthStatus(resourceHealth))
	}
}

func (aws Scraper) rds(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("RDS") {
		return
	}

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
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSRDSInstance, lo.FromPtr(instance.DBInstanceIdentifier))},
			Config:              instance,
			ConfigClass:         "RelationalDatabase",
			Name:                getName(labels, *instance.DBInstanceIdentifier),
			ID:                  *instance.DBInstanceIdentifier,
			Aliases:             []string{"AmazonRDS/" + *instance.DBInstanceArn},
			ParentExternalID:    *instance.DBSubnetGroup.VpcId,
			ParentType:          v1.AWSEC2VPC,
			RelationshipResults: relationships,
		}.WithHealthStatus(resourceHealth))
	}
}

func (aws Scraper) vpcs(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("VPC") {
		return
	}

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
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2VPC, lo.FromPtr(vpc.VpcId))},
			Config:              vpc,
			ConfigClass:         "VPC",
			Name:                getName(labels, *vpc.VpcId),
			ID:                  *vpc.VpcId,
			Aliases:             []string{"AmazonEC2/" + *vpc.VpcId},
			ParentExternalID:    lo.FromPtr(ctx.Caller.Account),
			ParentType:          v1.AWSAccount,
			RelationshipResults: relationships,
		}.WithHealthStatus(resourceHealth))
	}
}

func (aws Scraper) instances(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("EC2instance") {
		return
	}

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

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{*i.ImageId}, ConfigType: v1.AWSEC2AMI},
				RelatedExternalID: selfExternalID,
				Relationship:      "AMIInstance",
			})

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
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{ctx.Subnets[lo.FromPtr(i.SubnetId)].Zone}, ConfigType: v1.AWSZone},
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
				Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2Instance, instance.InstanceID)},
				Config:              instance,
				ConfigClass:         "VirtualMachine",
				Name:                instance.GetHostname(),
				Aliases:             []string{"AmazonEC2/" + instance.InstanceID},
				ID:                  instance.InstanceID,
				ParentExternalID:    instance.VpcID,
				ParentType:          v1.AWSEC2VPC,
				RelationshipResults: relationships,
			}.WithHealthStatus(resourceHealth))
		}
	}
}

func (aws Scraper) securityGroups(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("SecurityGroup") {
		return
	}

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
			Type:             v1.AWSEC2SecurityGroup,
			Labels:           labels,
			BaseScraper:      config.BaseScraper,
			Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2SecurityGroup, lo.FromPtr(sg.GroupId))},
			Config:           sg,
			ConfigClass:      "SecurityGroup",
			Name:             getName(labels, *sg.GroupId),
			ID:               *sg.GroupId,
			ParentExternalID: *sg.VpcId,
			ParentType:       v1.AWSEC2VPC,
		})
	}
}

func (aws Scraper) routes(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Route") {
		return
	}
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
			Type:             "AWS::EC2::RouteTable",
			Labels:           labels,
			BaseScraper:      config.BaseScraper,
			Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, "AWS::EC2::RouteTable", lo.FromPtr(r.RouteTableId))},
			Config:           r,
			ConfigClass:      "Route",
			Name:             getName(labels, *r.RouteTableId),
			ID:               *r.RouteTableId,
			ParentExternalID: *r.VpcId,
			ParentType:       v1.AWSEC2VPC,
		})
	}
}

func (aws Scraper) dhcp(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("DHCP") {
		return
	}

	describeInput := &ec2.DescribeDhcpOptionsInput{}
	describeOutput, err := ctx.EC2.DescribeDhcpOptions(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to describe dhcp options")
		return
	}

	for _, d := range describeOutput.DhcpOptions {
		labels := getLabels(d.Tags)
		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSEC2DHCPOptions,
			Labels:           labels,
			BaseScraper:      config.BaseScraper,
			Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2DHCPOptions, lo.FromPtr(d.DhcpOptionsId))},
			Config:           d,
			ConfigClass:      "DHCP",
			Name:             getName(labels, *d.DhcpOptionsId),
			ID:               *d.DhcpOptionsId,
			ParentExternalID: lo.FromPtr(ctx.Caller.Account),
			ParentType:       v1.AWSAccount,
		})
	}
}

func (aws Scraper) s3Buckets(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("S3Bucket") {
		return
	}

	S3 := s3.NewFromConfig(*ctx.Session)
	buckets, err := S3.ListBuckets(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to list s3 buckets")
		return
	}

	for _, bucket := range buckets.Buckets {
		labels := make(map[string]string)
		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSS3Bucket,
			CreatedAt:        bucket.CreationDate,
			BaseScraper:      config.BaseScraper,
			Config:           bucket,
			ConfigClass:      "ObjectStorage",
			Name:             *bucket.Name,
			Labels:           labels,
			Ignore:           []string{"name", "creationDate"},
			Aliases:          []string{"AmazonS3/" + *bucket.Name},
			ID:               *bucket.Name,
			ParentExternalID: *ctx.Caller.Account,
			ParentType:       v1.AWSAccount,
			Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSS3Bucket, lo.FromPtr(bucket.Name))},
		})
	}
}

func (aws Scraper) dnsZones(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("DNSZone") {
		return
	}
	Route53 := route53.NewFromConfig(*ctx.Session)
	zones, err := Route53.ListHostedZones(ctx, nil)
	if err != nil {
		results.Errorf(err, "failed to describe hosted zones")
		return
	}
	for _, zone := range zones.HostedZones {
		labels := make(map[string]string)
		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSZone,
			BaseScraper:      config.BaseScraper,
			Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSZone, strings.ReplaceAll(*zone.Id, "/hostedzone/", ""))},
			Config:           zone,
			ConfigClass:      "DNSZone",
			Name:             *zone.Name,
			Labels:           labels,
			Aliases:          []string{*zone.Id, *zone.Name, "AmazonRoute53/arn:aws:route53:::hostedzone/" + *zone.Id},
			ID:               strings.ReplaceAll(*zone.Id, "/hostedzone/", ""),
			ParentExternalID: *ctx.Caller.Account,
			ParentType:       v1.AWSAccount,
		})
	}
}

func (aws Scraper) loadBalancers(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("LoadBalancer") {
		return
	}
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
					ExternalID: []string{*lb.LoadBalancerName},
					ConfigType: v1.AWSLoadBalancer,
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID: []string{*instance.InstanceId},
					ConfigType: v1.AWSEC2Instance,
				},
				Relationship: "LoadBalancerInstance",
			})
		}

		clusterPrefix := "kubernetes.io/cluster/"
		elbTagsOutput, err := elb.DescribeTags(ctx, &elasticloadbalancing.DescribeTagsInput{LoadBalancerNames: []string{*lb.LoadBalancerName}})
		if err != nil {
			logger.Errorf("error while fetching elb tags: %v", err)
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
		arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", region, *ctx.Caller.Account, *lb.LoadBalancerName)
		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSLoadBalancer,
			CreatedAt:           lb.CreatedTime,
			Ignore:              []string{"createdTime"},
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSLoadBalancer, lo.FromPtr(lb.LoadBalancerName))},
			Config:              lb,
			ConfigClass:         "LoadBalancer",
			Name:                *lb.LoadBalancerName,
			Labels:              labels,
			Tags:                tags,
			Aliases:             []string{"AWSELB/" + arn, arn},
			ID:                  *lb.LoadBalancerName,
			ParentExternalID:    *lb.VPCId,
			ParentType:          v1.AWSEC2VPC,
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
			logger.Errorf("error while fetching elbv2 tags: %v", err)
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
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSLoadBalancerV2, lo.FromPtr(lb.LoadBalancerArn))},
			Ignore:              []string{"createdTime", "loadBalancerArn", "loadBalancerName"},
			CreatedAt:           lb.CreatedTime,
			Config:              lb,
			ConfigClass:         "LoadBalancer",
			Name:                *lb.LoadBalancerName,
			Aliases:             []string{"AWSELB/" + *lb.LoadBalancerArn},
			ID:                  *lb.LoadBalancerArn,
			Labels:              labels,
			ParentExternalID:    *lb.VpcId,
			ParentType:          v1.AWSEC2VPC,
			RelationshipResults: relationships,
		}.WithHealthStatus(resourceHealth))
	}
}

func (aws Scraper) subnets(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	// we always need to scrape subnets to get the zone for other resources
	subnets, err := ctx.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{})
	if err != nil {
		results.Errorf(err, "failed to get subnets")
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
			ParentExternalID:    lo.FromPtr(subnet.VpcId),
			ParentType:          v1.AWSEC2VPC,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2Subnet, lo.FromPtr(subnet.SubnetId))},
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
			Type:             v1.AWSIAMRole,
			CreatedAt:        role.CreateDate,
			BaseScraper:      config.BaseScraper,
			Properties:       []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMRole, lo.FromPtr(role.RoleName))},
			Config:           role,
			ConfigClass:      "Role",
			Labels:           labels,
			Name:             *role.RoleName,
			Aliases:          []string{*role.RoleName, *role.Arn},
			ID:               *role.RoleId,
			ParentExternalID: lo.FromPtr(ctx.Caller.Account),
			ParentType:       v1.AWSAccount,
		})
	}
}

func (aws Scraper) iamProfiles(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Profiles") {
		return
	}

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

		// We need to cast roles as []map[string]any to update the policy doc
		var profileRoles []map[string]any
		for _, r := range profileMap["Roles"].([]any) {
			profileRoles = append(profileRoles, r.(map[string]any))
		}
		profileMap["Roles"] = profileRoles

		for _, role := range profileMap["Roles"].([]map[string]any) {
			if val, exists := role["AssumeRolePolicyDocument"]; exists {
				policyDocEncoded := val.(string)
				doc, err := url.QueryUnescape(policyDocEncoded)
				if err != nil {
					logger.Errorf("error escaping policy doc[%s]: %v", policyDocEncoded, err)
					continue
				}
				docJSON, err := utils.ToJSONMap(doc)
				if err != nil {
					logger.Errorf("error dumping policy doc[%s] to json: %v", doc, err)
					continue
				}
				role["AssumeRolePolicyDocument"] = docJSON
			}
		}

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSIAMInstanceProfile,
			CreatedAt:           profile.CreateDate,
			BaseScraper:         config.BaseScraper,
			Properties:          []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMInstanceProfile, lo.FromPtr(profile.Arn))},
			Config:              profileMap,
			Labels:              labels,
			ConfigClass:         "Profile",
			Name:                *profile.InstanceProfileName,
			Aliases:             []string{*profile.InstanceProfileName, *profile.Arn},
			ID:                  *profile.InstanceProfileId,
			ParentExternalID:    lo.FromPtr(ctx.Caller.Account),
			ParentType:          v1.AWSAccount,
			RelationshipResults: relationships,
		})
	}
}

//nolint:all
func (aws Scraper) ami(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("Images") {
		return
	}

	amis, err := ctx.EC2.DescribeImages(ctx, &ec2.DescribeImagesInput{})
	if err != nil {
		results.Errorf(err, "failed to get amis")
		return
	}

	for _, image := range amis.Images {
		createdAt, err := time.Parse(time.RFC3339, *image.CreationDate)
		if err != nil {
			createdAt = time.Now()
		}

		labels := make(map[string]string)
		labels["region"] = *ctx.Caller.Account
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSEC2AMI,
			CreatedAt:   &createdAt,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSEC2AMI, lo.FromPtr(image.ImageId))},
			Config:      image,
			Labels:      labels,
			ConfigClass: "Image",
			Name:        ptr.ToString(image.Name),
			ID:          *image.ImageId,
		})
	}
}

func (aws Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.AWS) > 0
}

func (aws Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	allResults := v1.ScrapeResults{}

	for _, awsConfig := range ctx.ScrapeConfig().Spec.AWS {
		results := &v1.ScrapeResults{}
		for _, region := range awsConfig.Region {
			awsCtx, err := aws.getContext(ctx, awsConfig, region)
			if err != nil {
				results.Errorf(err, "failed to create AWS context")
				allResults = append(allResults, *results...)
				continue
			}

			ctx.Logger.V(1).Infof("scraping %s", awsCtx)
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
			aws.containerImages(awsCtx, awsConfig, results)
			aws.cloudtrail(awsCtx, awsConfig, results)
			aws.availabilityZones(awsCtx, awsConfig, results)
			// We are querying half a million amis, need to optimize for this
			// aws.ami(awsCtx, awsConfig, results)
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

		for i := range *results {
			(*results)[i].Tags = append((*results)[i].Tags, v1.Tag{
				Name:  "account",
				Value: lo.FromPtr(awsCtx.Caller.Account),
			})
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

func getConsoleLink(region, resourceType, resourceID string) *types.Property {
	var url string
	switch resourceType {
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
