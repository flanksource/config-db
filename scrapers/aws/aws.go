package aws

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	"github.com/aws/aws-sdk-go-v2/service/support"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/pkg/errors"
)

// Scraper ...
type Scraper struct {
}

type AWSContext struct {
	*v1.ScrapeContext
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

func getTags(tags []types.Tag) v1.JSONStringMap {
	result := make(v1.JSONStringMap)
	for _, tag := range tags {
		result[*tag.Key] = *tag.Value
	}
	return result
}

func (ctx AWSContext) String() string {
	return fmt.Sprintf("account=%s user=%s region=%s", *ctx.Caller.Account, *ctx.Caller.UserId, ctx.Session.Region)
}

func (aws Scraper) getContext(ctx *v1.ScrapeContext, awsConfig v1.AWS, region string) (*AWSContext, error) {
	session, err := NewSession(ctx, *awsConfig.AWSConnection, region)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create AWS session")
	}
	STS := sts.NewFromConfig(*session)
	caller, err := STS.GetCallerIdentity(ctx, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get identity")
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
	for _, image := range images.Repositories {
		*results = append(*results, v1.ScrapeResult{
			CreatedAt:    image.CreatedAt,
			ExternalType: "AWS::ECR::Repository",
			BaseScraper:  config.BaseScraper,
			Config:       image,
			Type:         "Container",
			Name:         *image.RepositoryName,
			Aliases:      []string{*image.RepositoryArn, "AmazonECR/" + *image.RepositoryArn},
			Account:      *ctx.Caller.Account,
			ID:           *image.RepositoryUri,
			Ignore: []string{
				"CreatedAt", "RepositoryArn", "RepositoryUri", "RegistryId", "RepositoryName",
			},
			ParentExternalID:   *ctx.Caller.Account,
			ParentExternalType: v1.AWSAccount,
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

		*results = append(*results, v1.ScrapeResult{
			ExternalType:       v1.AWSEKSCluster,
			CreatedAt:          cluster.Cluster.CreatedAt,
			Tags:               cluster.Cluster.Tags,
			BaseScraper:        config.BaseScraper,
			Config:             cluster.Cluster,
			Type:               "EKS",
			Network:            *cluster.Cluster.ResourcesVpcConfig.VpcId,
			Name:               getName(cluster.Cluster.Tags, clusterName),
			Account:            *ctx.Caller.Account,
			Aliases:            []string{*cluster.Cluster.Arn, "AmazonEKS/" + *cluster.Cluster.Arn},
			ID:                 *cluster.Cluster.Name,
			Ignore:             []string{"createdAt", "name"},
			ParentExternalID:   *cluster.Cluster.ResourcesVpcConfig.VpcId,
			ParentExternalType: v1.AWSEC2VPC,
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

		*results = append(*results, v1.ScrapeResult{
			ExternalType: "AWS::EFS::FileSystem",
			Tags:         tags,
			BaseScraper:  config.BaseScraper,
			Config:       fs,
			Type:         "EFS",
			Name:         getName(tags, *fs.FileSystemId),
			Account:      *ctx.Caller.Account,
			ID:           *fs.FileSystemId,
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

	*results = append(*results, v1.ScrapeResult{
		ExternalType: v1.AWSAccount,
		BaseScraper:  config.BaseScraper,
		Config:       summary.SummaryMap,
		Type:         "Account",
		Name:         name,
		Account:      *ctx.Caller.Account,
		Aliases:      aliases.AccountAliases,
		ID:           *ctx.Caller.Account,
	})

	*results = append(*results, v1.ScrapeResult{
		ExternalType: "AWS::IAM::User",
		BaseScraper:  config.BaseScraper,
		Config:       summary.SummaryMap,
		Type:         "User",
		Name:         "root",
		Account:      *ctx.Caller.Account,
		Aliases:      []string{"<root account>"},
		ID:           "root",
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
			ExternalType: "AWS::Region",
			Type:         "Region",
			BaseScraper:  config.BaseScraper,
			Config:       region,
			Name:         *region.RegionName,
			ID:           *region.RegionName,
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

	for _, user := range users.Users {
		*results = append(*results, v1.ScrapeResult{
			ExternalType: "AWS::IAM::User",
			CreatedAt:    user.CreateDate,
			BaseScraper:  config.BaseScraper,
			Config:       user,
			Type:         "User",
			Name:         *user.UserName,
			Account:      *ctx.Caller.Account,
			Aliases:      []string{*user.UserId, *user.Arn},
			Ignore:       []string{"arn", "userId", "createDate", "userName"},
			ID:           *user.UserName, // UserId is not often referenced
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
		*results = append(*results, v1.ScrapeResult{
			ExternalType: v1.AWSEBSVolume,
			Tags:         tags,
			BaseScraper:  config.BaseScraper,
			Config:       volume,
			Type:         "EBS",
			Aliases:      []string{"AmazonEC2/" + *volume.VolumeId},
			Name:         getName(tags, *volume.VolumeId),
			Account:      *ctx.Caller.Account,
			ID:           *volume.VolumeId,
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
					ExternalID:   []string{*instance.DBInstanceIdentifier},
					ExternalType: v1.AWSRDSInstance,
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID:   []string{*sg.VpcSecurityGroupId},
					ExternalType: v1.AWSEC2SecurityGroup,
				},
				Relationship: "RDSSecurityGroup",
			})
		}

		*results = append(*results, v1.ScrapeResult{
			ExternalType:        v1.AWSRDSInstance,
			Tags:                tags,
			BaseScraper:         config.BaseScraper,
			Config:              instance,
			Type:                "RDS",
			Name:                getName(tags, *instance.DBInstanceIdentifier),
			Account:             *ctx.Caller.Account,
			ID:                  *instance.DBInstanceIdentifier,
			Aliases:             []string{"AmazonRDS/" + *instance.DBInstanceArn},
			ParentExternalID:    *instance.DBSubnetGroup.VpcId,
			ParentExternalType:  v1.AWSEC2VPC,
			RelationshipResults: relationships,
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
				ExternalID:   []string{*vpc.VpcId},
				ExternalType: v1.AWSEC2VPC,
			},
			RelatedExternalID: v1.ExternalID{
				ExternalID:   []string{*vpc.DhcpOptionsId},
				ExternalType: v1.AWSEC2DHCPOptions,
			},
			Relationship: "VPCDHCPOptions",
		})

		tags := getTags(vpc.Tags)
		*results = append(*results, v1.ScrapeResult{
			ExternalType:        v1.AWSEC2VPC,
			Tags:                tags,
			BaseScraper:         config.BaseScraper,
			Config:              vpc,
			Type:                "VPC",
			Network:             *vpc.VpcId,
			Name:                getName(tags, *vpc.VpcId),
			Account:             *ctx.Caller.Account,
			ID:                  *vpc.VpcId,
			Aliases:             []string{"AmazonEC2/" + *vpc.VpcId},
			ParentExternalID:    *ctx.Caller.Account,
			ParentExternalType:  v1.AWSAccount,
			RelationshipResults: relationships,
		})
	}
}

func (aws Scraper) instances(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {

	if !config.Includes("EC2instance") {
		return
	}

	describeInput := &ec2.DescribeInstancesInput{}

	describeOutput, err := ctx.EC2.DescribeInstances(ctx, describeInput)
	if err != nil {
		results.Errorf(err, "failed to describe instances")
		return
	}

	var relationships v1.RelationshipResults
	for _, r := range describeOutput.Reservations {
		for _, i := range r.Instances {
			instance := NewInstance(i)

			selfExternalID := v1.ExternalID{
				ExternalID:   []string{*i.InstanceId},
				ExternalType: v1.AWSEC2Instance,
			}
			// SecurityGroup relationships
			for _, sg := range i.SecurityGroups {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID: selfExternalID,
					RelatedExternalID: v1.ExternalID{
						ExternalID:   []string{*sg.GroupId},
						ExternalType: v1.AWSEC2SecurityGroup,
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
							ExternalID:   []string{*i.InstanceId},
							ExternalType: v1.AWSEC2Instance,
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
						ExternalID:   []string{*vol.Ebs.VolumeId},
						ExternalType: v1.AWSEBSVolume,
					},
					Relationship: "AttachedVolume",
				})
			}

			if i.IamInstanceProfile != nil {
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID: selfExternalID,
					RelatedExternalID: v1.ExternalID{
						ExternalID:   []string{*i.IamInstanceProfile.Id},
						ExternalType: v1.AWSIAMInstanceProfile,
					},
					Relationship: "IAMInstanceProfile",
				})
			}

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID: selfExternalID,
				RelatedExternalID: v1.ExternalID{
					ExternalID:   []string{*i.ImageId},
					ExternalType: v1.AWSEC2AMI,
				},
				Relationship: "InstanceAMI",
			})

			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID: selfExternalID,
				RelatedExternalID: v1.ExternalID{
					ExternalID:   []string{"Kubernetes/Node//" + instance.GetHostname()},
					ExternalType: v1.AWSEC2AMI,
				},
				Relationship: "Instance-KuberenetesNode",
			})

			*results = append(*results, v1.ScrapeResult{
				ExternalType:        v1.AWSEC2Instance,
				Tags:                instance.Tags,
				BaseScraper:         config.BaseScraper,
				Config:              instance,
				Type:                "EC2Instance",
				Network:             instance.VpcID,
				Subnet:              instance.SubnetID,
				Zone:                ctx.Subnets[instance.SubnetID].Zone,
				Region:              ctx.Subnets[instance.SubnetID].Region,
				Name:                instance.GetHostname(),
				Account:             *ctx.Caller.Account,
				Aliases:             []string{"AmazonEC2/" + instance.InstanceID},
				ID:                  instance.InstanceID,
				ParentExternalID:    instance.SubnetID,
				ParentExternalType:  v1.AWSEC2Subnet,
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
		*results = append(*results, v1.ScrapeResult{
			ExternalType:       "AWS::EC2::SecurityGroup",
			Tags:               tags,
			BaseScraper:        config.BaseScraper,
			Config:             sg,
			Type:               "SecurityGroup",
			Network:            *sg.VpcId,
			Name:               getName(tags, *sg.GroupId),
			Account:            *ctx.Caller.Account,
			ID:                 *sg.GroupId,
			ParentExternalID:   *sg.VpcId,
			ParentExternalType: v1.AWSEC2VPC,
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
		*results = append(*results, v1.ScrapeResult{
			ExternalType:       "AWS::EC2::RouteTable",
			Tags:               tags,
			BaseScraper:        config.BaseScraper,
			Config:             r,
			Type:               "Route",
			Network:            *r.VpcId,
			Name:               getName(tags, *r.RouteTableId),
			Account:            *ctx.Caller.Account,
			ID:                 *r.RouteTableId,
			ParentExternalID:   *r.VpcId,
			ParentExternalType: v1.AWSEC2VPC,
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
		*results = append(*results, v1.ScrapeResult{
			ExternalType: v1.AWSEC2DHCPOptions,
			Tags:         tags,
			BaseScraper:  config.BaseScraper,
			Config:       d,
			Type:         "DHCP",
			Name:         getName(tags, *d.DhcpOptionsId),
			Account:      *ctx.Caller.Account,
			ID:           *d.DhcpOptionsId,
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
		*results = append(*results, v1.ScrapeResult{
			ExternalType:       v1.AWSS3Bucket,
			CreatedAt:          bucket.CreationDate,
			BaseScraper:        config.BaseScraper,
			Config:             bucket,
			Type:               "S3Bucket",
			Name:               *bucket.Name,
			Ignore:             []string{"name", "creationDate"},
			Aliases:            []string{"AmazonS3/" + *bucket.Name},
			ID:                 *bucket.Name,
			ParentExternalID:   *ctx.Caller.Account,
			ParentExternalType: v1.AWSAccount,
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
		*results = append(*results, v1.ScrapeResult{
			ExternalType:       "AWS::Route53::HostedZone",
			BaseScraper:        config.BaseScraper,
			Config:             zone,
			Type:               "DNSZone",
			Name:               *zone.Name,
			Account:            *ctx.Caller.Account,
			Aliases:            []string{*zone.Id, *zone.Name, "AmazonRoute53/arn:aws:route53:::hostedzone/" + *zone.Id},
			ID:                 strings.ReplaceAll(*zone.Id, "/hostedzone/", ""),
			ParentExternalID:   *ctx.Caller.Account,
			ParentExternalType: v1.AWSAccount,
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
					ExternalID:   []string{*lb.LoadBalancerName},
					ExternalType: v1.AWSLoadBalancer,
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID:   []string{*instance.InstanceId},
					ExternalType: v1.AWSEC2Instance,
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
								ExternalID:   []string{*lb.LoadBalancerName},
								ExternalType: v1.AWSLoadBalancer,
							},
							RelatedExternalID: v1.ExternalID{
								ExternalID:   []string{clusterName},
								ExternalType: v1.AWSEKSCluster,
							},
							Relationship: "EKSLoadBalancer",
						})
					}
				}
			}
		}

		az := lb.AvailabilityZones[0]
		region := az[:len(az)-1]
		arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", region, *ctx.Caller.Account, *lb.LoadBalancerName)
		*results = append(*results, v1.ScrapeResult{
			ExternalType:        v1.AWSLoadBalancer,
			CreatedAt:           lb.CreatedTime,
			Ignore:              []string{"createdTime"},
			BaseScraper:         config.BaseScraper,
			Config:              lb,
			Type:                "LoadBalancer",
			Name:                *lb.LoadBalancerName,
			Account:             *ctx.Caller.Account,
			Region:              region,
			Aliases:             []string{"AWSELB/" + arn, arn},
			ID:                  *lb.LoadBalancerName,
			ParentExternalID:    *lb.VPCId,
			ParentExternalType:  v1.AWSEC2VPC,
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
								ExternalID:   []string{*lb.LoadBalancerArn},
								ExternalType: v1.AWSLoadBalancerV2,
							},
							RelatedExternalID: v1.ExternalID{
								ExternalID:   []string{clusterName},
								ExternalType: v1.AWSEKSCluster,
							},
							Relationship: "EKSLoadBalancer",
						})
					}
				}
			}
		}

		*results = append(*results, v1.ScrapeResult{
			ExternalType:        v1.AWSLoadBalancerV2,
			BaseScraper:         config.BaseScraper,
			Ignore:              []string{"createdTime", "loadBalancerArn", "loadBalancerName"},
			CreatedAt:           lb.CreatedTime,
			Config:              lb,
			Type:                "LoadBalancer",
			Name:                *lb.LoadBalancerName,
			Account:             *ctx.Caller.Account,
			Aliases:             []string{"AWSELB/" + *lb.LoadBalancerArn},
			ID:                  *lb.LoadBalancerArn,
			ParentExternalID:    *lb.VpcId,
			ParentExternalType:  v1.AWSEC2VPC,
			RelationshipResults: relationships,
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

		ctx.Subnets[*subnet.SubnetId] = Zone{Zone: az, Region: az[0 : len(az)-1]}

		if !config.Includes("subnet") {
			return
		}
		result := v1.ScrapeResult{
			ExternalType:       "AWS::EC2::Subnet",
			BaseScraper:        config.BaseScraper,
			Tags:               tags,
			Type:               "Subnet",
			ID:                 *subnet.SubnetId,
			Subnet:             *subnet.SubnetId,
			Config:             subnet,
			Account:            *ctx.Caller.Account,
			Network:            *subnet.VpcId,
			Zone:               az,
			Region:             az[0 : len(az)-1],
			ParentExternalID:   *subnet.VpcId,
			ParentExternalType: v1.AWSEC2VPC,
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
		*results = append(*results, v1.ScrapeResult{
			ExternalType: v1.AWSIAMRole,
			CreatedAt:    role.CreateDate,
			BaseScraper:  config.BaseScraper,
			Config:       role,
			Type:         "Role",
			Name:         *role.RoleName,
			Account:      *ctx.Caller.Account,
			Aliases:      []string{*role.RoleName, *role.Arn},
			ID:           *role.RoleId,
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

	for _, profile := range profiles.InstanceProfiles {
		*results = append(*results, v1.ScrapeResult{
			ExternalType: v1.AWSIAMInstanceProfile,
			CreatedAt:    profile.CreateDate,
			BaseScraper:  config.BaseScraper,
			Config:       profile,
			Type:         "Profile",
			Name:         *profile.InstanceProfileName,
			Account:      *ctx.Caller.Account,
			Aliases:      []string{*profile.InstanceProfileName, *profile.Arn},
			ID:           *profile.InstanceProfileId,
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

		*results = append(*results, v1.ScrapeResult{
			ExternalType: v1.AWSEC2AMI,
			CreatedAt:    &createdAt,
			BaseScraper:  config.BaseScraper,
			Config:       image,
			Type:         "Image",
			Name:         ptr.ToString(image.Name),
			Account:      *ctx.Caller.Account,
			ID:           *image.ImageId,
		})
	}
}

// Scrape ...
func (aws Scraper) Scrape(ctx *v1.ScrapeContext, config v1.ConfigScraper) v1.ScrapeResults {
	results := &v1.ScrapeResults{}

	for _, awsConfig := range config.AWS {
		for _, region := range awsConfig.Region {
			awsCtx, err := aws.getContext(ctx, awsConfig, region)
			if err != nil {
				return results.Errorf(err, "failed to create AWS context")
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
			aws.cloudtrail(awsCtx, awsConfig, results)
			aws.loadBalancers(awsCtx, awsConfig, results)
			aws.containerImages(awsCtx, awsConfig, results)
			// We are querying half a million amis, need to optimize for this
			// aws.ami(awsCtx, awsConfig, results)
		}

		awsCtx, err := aws.getContext(ctx, awsConfig, "us-east-1")
		if err != nil {
			return results.Errorf(err, "failed to create AWS context")
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

func getExternalTypeById(id string) string {
	prefix := strings.Split(id, "-")[0]
	switch prefix {
	case "i":
		return "AWS::EC2::Instance"
	case "db":
		return "AWS::RDS::DBInstance"
	case "sg":
		return "AWS::EC2::SecurityGroup"
	case "vol":
		return "AWS::EBS::Volume"
	case "vpc":
		return "AWS::EC2::VPC"
	case "subnet":
		return "AWS::EC2::Subnet"
	}
	return ""

}
