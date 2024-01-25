package aws

import (
	"fmt"
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

func getTags(tags []ec2Types.Tag) v1.JSONStringMap {
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
	session, err := NewSession(ctx, *awsConfig.AWSConnection, region)
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
	tags := make(map[string]string)
	tags["account"] = *ctx.Caller.Account
	for _, image := range images.Repositories {
		*results = append(*results, v1.ScrapeResult{
			CreatedAt:   image.CreatedAt,
			Type:        "AWS::ECR::Repository",
			BaseScraper: config.BaseScraper,
			Config:      image,
			Tags:        tags,
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

		cluster.Cluster.Tags["account"] = *ctx.Caller.Account
		cluster.Cluster.Tags["region"] = getRegionFromArn(*cluster.Cluster.Arn, "eks")

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSEKSCluster,
			CreatedAt:           cluster.Cluster.CreatedAt,
			Tags:                cluster.Cluster.Tags,
			BaseScraper:         config.BaseScraper,
			Config:              cluster.Cluster,
			ConfigClass:         "KubernetesCluster",
			Name:                getName(cluster.Cluster.Tags, clusterName),
			Aliases:             []string{*cluster.Cluster.Arn, "AmazonEKS/" + *cluster.Cluster.Arn},
			ID:                  *cluster.Cluster.Name,
			Ignore:              []string{"createdAt", "name"},
			ParentExternalID:    *cluster.Cluster.ResourcesVpcConfig.VpcId,
			ParentType:          v1.AWSEC2VPC,
			RelationshipResults: relationships,
			Status:              health.MapAWSStatus(string(cluster.Cluster.Status)),
		})
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
		tags := make(v1.JSONStringMap)
		for _, tag := range fs.Tags {
			tags[*tag.Key] = *tag.Value
		}
		tags["account"] = *ctx.Caller.Account

		*results = append(*results, v1.ScrapeResult{
			Type:        "AWS::EFS::FileSystem",
			Tags:        tags,
			BaseScraper: config.BaseScraper,
			Config:      fs,
			ConfigClass: "FileSystem",
			Name:        getName(tags, *fs.FileSystemId),
			ID:          *fs.FileSystemId,
		})
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

	tags := make(map[string]string)
	tags["account"] = *ctx.Caller.Account
	*results = append(*results, v1.ScrapeResult{
		Type:        v1.AWSAccount,
		BaseScraper: config.BaseScraper,
		Config:      summary.SummaryMap,
		ConfigClass: "Account",
		Name:        name,
		Tags:        tags,
		Aliases:     aliases.AccountAliases,
		ID:          *ctx.Caller.Account,
	})

	*results = append(*results, v1.ScrapeResult{
		Type:             "AWS::IAM::User",
		BaseScraper:      config.BaseScraper,
		Config:           summary.SummaryMap,
		Tags:             tags,
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
			Tags:        tags,
			ID:          *region.RegionName,
		})
	}

	azDescribeInput := &ec2.DescribeAvailabilityZonesInput{}
	azDescribeOutput, err := ctx.EC2.DescribeAvailabilityZones(ctx, azDescribeInput)
	if err != nil {
		results.Errorf(err, "failed to describe availability zones")
		return
	}

	for _, az := range azDescribeOutput.AvailabilityZones {
		*results = append(*results, v1.ScrapeResult{
			ID:               lo.FromPtr(az.ZoneId),
			Type:             v1.AWSAvailabilityZone,
			BaseScraper:      config.BaseScraper,
			Config:           az,
			ConfigClass:      "AvailabilityZone",
			Aliases:          []string{lo.FromPtr(az.ZoneName)},
			Name:             lo.FromPtr(az.ZoneName),
			ParentExternalID: lo.FromPtr(az.RegionName),
			ParentType:       v1.AWSRegion,
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

	tags := make(map[string]string)
	tags["account"] = *ctx.Caller.Account
	for _, user := range users.Users {
		*results = append(*results, v1.ScrapeResult{
			Type:             "AWS::IAM::User",
			CreatedAt:        user.CreateDate,
			BaseScraper:      config.BaseScraper,
			Config:           user,
			ConfigClass:      "User",
			Tags:             tags,
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
		tags := getTags(volume.Tags)
		tags["account"] = *ctx.Caller.Account
		tags["zone"] = *volume.AvailabilityZone
		// Remove last letter from zone
		tags["region"] = tags["zone"][:len(tags["zone"])-1]
		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSEBSVolume,
			Tags:             tags,
			BaseScraper:      config.BaseScraper,
			Config:           volume,
			ConfigClass:      "DiskStorage",
			Aliases:          []string{"AmazonEC2/" + *volume.VolumeId},
			Name:             getName(tags, *volume.VolumeId),
			ID:               *volume.VolumeId,
			ParentExternalID: lo.FromPtr(ctx.Caller.Account),
			ParentType:       v1.AWSAccount,
			Status:           health.MapAWSStatus(string(volume.State)),
		})
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
		tags := make(v1.JSONStringMap)
		for _, tag := range instance.TagList {
			tags[*tag.Key] = *tag.Value
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

		tags["account"] = *ctx.Caller.Account
		tags["region"] = getRegionFromArn(*instance.DBInstanceArn, "rds")
		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSRDSInstance,
			Tags:                tags,
			BaseScraper:         config.BaseScraper,
			Config:              instance,
			ConfigClass:         "RelationalDatabase",
			Name:                getName(tags, *instance.DBInstanceIdentifier),
			ID:                  *instance.DBInstanceIdentifier,
			Aliases:             []string{"AmazonRDS/" + *instance.DBInstanceArn},
			ParentExternalID:    *instance.DBSubnetGroup.VpcId,
			ParentType:          v1.AWSEC2VPC,
			RelationshipResults: relationships,
			Status:              health.MapAWSStatus(lo.FromPtr(instance.DBInstanceStatus)),
		})
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

		tags := getTags(vpc.Tags)
		tags["account"] = *ctx.Caller.Account
		tags["network"] = *vpc.VpcId
		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSEC2VPC,
			Tags:                tags,
			BaseScraper:         config.BaseScraper,
			Config:              vpc,
			ConfigClass:         "VPC",
			Name:                getName(tags, *vpc.VpcId),
			ID:                  *vpc.VpcId,
			Aliases:             []string{"AmazonEC2/" + *vpc.VpcId},
			ParentExternalID:    lo.FromPtr(ctx.Caller.Account),
			ParentType:          v1.AWSAccount,
			RelationshipResults: relationships,
			Status:              health.MapAWSStatus(string(vpc.State)),
		})
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
					ConfigExternalID: selfExternalID,
					RelatedExternalID: v1.ExternalID{
						ExternalID: []string{*sg.GroupId},
						ConfigType: v1.AWSEC2SecurityGroup,
					},
					Relationship: "InstanceSecurityGroup",
				})
			}

			// Cluster node relationships
			for _, tag := range i.Tags {
				if *tag.Key == "aws:eks:cluster-name" {
					relationships = append(relationships, v1.RelationshipResult{
						ConfigExternalID: selfExternalID,
						RelatedExternalID: v1.ExternalID{
							ExternalID: []string{*tag.Value},
							ConfigType: v1.AWSEKSCluster,
						},
						Relationship: "EKSNode",
					})
				}
			}

			// Volume relationships
			for _, vol := range i.BlockDeviceMappings {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID: selfExternalID,
					RelatedExternalID: v1.ExternalID{
						ExternalID: []string{*vol.Ebs.VolumeId},
						ConfigType: v1.AWSEBSVolume,
					},
					Relationship: "AttachedVolume",
				})
			}

			if i.IamInstanceProfile != nil {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID: selfExternalID,
					RelatedExternalID: v1.ExternalID{
						ExternalID: []string{*i.IamInstanceProfile.Id},
						ConfigType: v1.AWSIAMInstanceProfile,
					},
					Relationship: "IAMInstanceProfile",
				})
			}

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID: selfExternalID,
				RelatedExternalID: v1.ExternalID{
					ExternalID: []string{*i.ImageId},
					ConfigType: v1.AWSEC2AMI,
				},
				Relationship: "InstanceAMI",
			})

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID: selfExternalID,
				RelatedExternalID: v1.ExternalID{
					ExternalID: []string{"Kubernetes/Node//" + *i.PrivateDnsName},
					ConfigType: "Kubernetes::Node",
				},
				Relationship: "Instance-KuberenetesNode",
			})

			// Instance to Subnet relationship
			relationships = append(relationships, v1.RelationshipResult{
				RelatedExternalID: selfExternalID,
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{lo.FromPtr(i.SubnetId)}, ConfigType: v1.AWSEC2Subnet},
				Relationship:      "SubnetInstance",
			})

			// Instance to Region relationship
			relationships = append(relationships, v1.RelationshipResult{
				RelatedExternalID: selfExternalID,
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{ctx.Session.Region}, ConfigType: v1.AWSRegion},
				Relationship:      "RegionInstance",
			})

			// Instance to zone relationship
			relationships = append(relationships, v1.RelationshipResult{
				RelatedExternalID: selfExternalID,
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{ctx.Subnets[lo.FromPtr(i.SubnetId)].Zone}, ConfigType: v1.AWSZone},
				Relationship:      "ZoneInstance",
			})

			instance := NewInstance(i)
			tags := instance.Tags
			if tags == nil {
				tags = make(map[string]string)
			}
			tags["zone"] = ctx.Subnets[instance.SubnetID].Zone
			tags["region"] = ctx.Subnets[instance.SubnetID].Region
			tags["account"] = *ctx.Caller.Account
			tags["network"] = instance.VpcID
			tags["subnet"] = instance.SubnetID

			*results = append(*results, v1.ScrapeResult{
				Type:                v1.AWSEC2Instance,
				Status:              health.MapAWSStatus(string(i.State.Name)),
				Tags:                tags,
				BaseScraper:         config.BaseScraper,
				Config:              instance,
				ConfigClass:         "VirtualMachine",
				Name:                instance.GetHostname(),
				Aliases:             []string{"AmazonEC2/" + instance.InstanceID},
				ID:                  instance.InstanceID,
				ParentExternalID:    instance.VpcID,
				ParentType:          v1.AWSEC2VPC,
				RelationshipResults: relationships,
			})
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
		tags := getTags(sg.Tags)
		tags["account"] = *ctx.Caller.Account
		tags["network"] = *sg.VpcId
		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSEC2SecurityGroup,
			Tags:             tags,
			BaseScraper:      config.BaseScraper,
			Config:           sg,
			ConfigClass:      "SecurityGroup",
			Name:             getName(tags, *sg.GroupId),
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
		tags := getTags(r.Tags)
		tags["account"] = *ctx.Caller.Account
		tags["network"] = *r.VpcId
		*results = append(*results, v1.ScrapeResult{
			Type:             "AWS::EC2::RouteTable",
			Tags:             tags,
			BaseScraper:      config.BaseScraper,
			Config:           r,
			ConfigClass:      "Route",
			Name:             getName(tags, *r.RouteTableId),
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
		tags := getTags(d.Tags)
		tags["account"] = *ctx.Caller.Account
		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSEC2DHCPOptions,
			Tags:             tags,
			BaseScraper:      config.BaseScraper,
			Config:           d,
			ConfigClass:      "DHCP",
			Name:             getName(tags, *d.DhcpOptionsId),
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
		tags := make(map[string]string)
		tags["account"] = *ctx.Caller.Account
		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSS3Bucket,
			CreatedAt:        bucket.CreationDate,
			BaseScraper:      config.BaseScraper,
			Config:           bucket,
			ConfigClass:      "ObjectStorage",
			Name:             *bucket.Name,
			Tags:             tags,
			Ignore:           []string{"name", "creationDate"},
			Aliases:          []string{"AmazonS3/" + *bucket.Name},
			ID:               *bucket.Name,
			ParentExternalID: *ctx.Caller.Account,
			ParentType:       v1.AWSAccount,
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
		tags := make(map[string]string)
		tags["account"] = *ctx.Caller.Account
		*results = append(*results, v1.ScrapeResult{
			Type:             "AWS::Route53::HostedZone",
			BaseScraper:      config.BaseScraper,
			Config:           zone,
			ConfigClass:      "DNSZone",
			Name:             *zone.Name,
			Tags:             tags,
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
		tags := make(map[string]string)
		tags["zone"] = az
		tags["region"] = region
		tags["account"] = *ctx.Caller.Account
		arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", region, *ctx.Caller.Account, *lb.LoadBalancerName)
		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSLoadBalancer,
			CreatedAt:           lb.CreatedTime,
			Ignore:              []string{"createdTime"},
			BaseScraper:         config.BaseScraper,
			Config:              lb,
			ConfigClass:         "LoadBalancer",
			Name:                *lb.LoadBalancerName,
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
		tags := make(map[string]string)
		tags["account"] = *ctx.Caller.Account

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSLoadBalancerV2,
			BaseScraper:         config.BaseScraper,
			Ignore:              []string{"createdTime", "loadBalancerArn", "loadBalancerName"},
			CreatedAt:           lb.CreatedTime,
			Config:              lb,
			ConfigClass:         "LoadBalancer",
			Name:                *lb.LoadBalancerName,
			Aliases:             []string{"AWSELB/" + *lb.LoadBalancerArn},
			ID:                  *lb.LoadBalancerArn,
			Tags:                tags,
			ParentExternalID:    *lb.VpcId,
			ParentType:          v1.AWSEC2VPC,
			RelationshipResults: relationships,
			Status:              health.MapAWSStatus(string(lb.State.Code)),
		})
	}

}

func (aws Scraper) subnets(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	// we always need to scrape subnets to get the zone for other resources
	subnets, err := ctx.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{})
	if err != nil {
		results.Errorf(err, "failed to get subnets")
	}

	for _, subnet := range subnets.Subnets {
		// Subnet tags are of the form [{Key: "<key>", Value:
		// "<value>"}, ...]
		tags := make(v1.JSONStringMap)
		for _, tag := range subnet.Tags {
			tags[*tag.Key] = *tag.Value
		}

		az := *subnet.AvailabilityZone
		region := az[0 : len(az)-1]
		ctx.Subnets[*subnet.SubnetId] = Zone{Zone: az, Region: region}
		tags["zone"] = az
		tags["region"] = region
		tags["account"] = *ctx.Caller.Account
		tags["network"] = *subnet.VpcId
		tags["subnet"] = *subnet.SubnetId

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

		result := v1.ScrapeResult{
			Type:                v1.AWSEC2Subnet,
			BaseScraper:         config.BaseScraper,
			Tags:                tags,
			ConfigClass:         "Subnet",
			ID:                  *subnet.SubnetId,
			Config:              subnet,
			ParentExternalID:    lo.FromPtr(subnet.VpcId),
			ParentType:          v1.AWSEC2VPC,
			Status:              health.MapAWSStatus(string(subnet.State)),
			RelationshipResults: relationships,
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
		tags := make(map[string]string)
		tags["account"] = *ctx.Caller.Account

		*results = append(*results, v1.ScrapeResult{
			Type:             v1.AWSIAMRole,
			CreatedAt:        role.CreateDate,
			BaseScraper:      config.BaseScraper,
			Config:           role,
			ConfigClass:      "Role",
			Tags:             tags,
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

	tags := make(map[string]string)
	tags["account"] = *ctx.Caller.Account
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

		*results = append(*results, v1.ScrapeResult{
			Type:                v1.AWSIAMInstanceProfile,
			CreatedAt:           profile.CreateDate,
			BaseScraper:         config.BaseScraper,
			Config:              profile,
			Tags:                tags,
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

		tags := make(map[string]string)
		tags["region"] = *ctx.Caller.Account
		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSEC2AMI,
			CreatedAt:   &createdAt,
			BaseScraper: config.BaseScraper,
			Config:      image,
			Tags:        tags,
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
	results := &v1.ScrapeResults{}

	for _, awsConfig := range ctx.ScrapeConfig().Spec.AWS {
		for _, region := range awsConfig.Region {
			awsCtx, err := aws.getContext(ctx, awsConfig, region)
			if err != nil {
				results.Errorf(err, "failed to create AWS context")
				continue
			}

			logger.Infof("Scrapping %s", awsCtx)
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
			// We are querying half a million amis, need to optimize for this
			// aws.ami(awsCtx, awsConfig, results)
		}

		awsCtx, err := aws.getContext(ctx, awsConfig, "us-east-1")
		if err != nil {
			results.Errorf(err, "failed to create AWS context")
			continue
		}

		aws.account(awsCtx, awsConfig, results)
		aws.users(awsCtx, awsConfig, results)
		aws.iamRoles(awsCtx, awsConfig, results)
		aws.iamProfiles(awsCtx, awsConfig, results)
		aws.dnsZones(awsCtx, awsConfig, results)
		aws.trustedAdvisor(awsCtx, awsConfig, results)
		aws.s3Buckets(awsCtx, awsConfig, results)
	}

	return *results
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
