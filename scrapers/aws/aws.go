package aws

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/support"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
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
	return fmt.Sprintf("account=%s user=%s", *ctx.Caller.Account, *ctx.Caller.UserId)
}

func (aws Scraper) getContext(ctx v1.ScrapeContext, awsConfig v1.AWS) (*AWSContext, error) {

	session, err := NewSession(&ctx, *awsConfig.AWSConnection)
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
		ScrapeContext: &ctx,
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

func getKeys(instances map[string]*Instance) []string {
	ids := []string{}
	for id := range instances {
		ids = append(ids, id)
	}
	return ids
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
			ExternalType: "AWS::EKS::Cluster",
			Tags:         cluster.Cluster.Tags,
			BaseScraper:  config.BaseScraper,
			Config:       cluster.Cluster,
			Type:         "EKS",
			Network:      *cluster.Cluster.ResourcesVpcConfig.VpcId,
			Name:         getName(cluster.Cluster.Tags, clusterName),
			Account:      *ctx.Caller.Account,
			ID:           *cluster.Cluster.Arn})
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
		ExternalType: "AWS::::Account",
		BaseScraper:  config.BaseScraper,
		Config:       summary.SummaryMap,
		Type:         "Account",
		Name:         name,
		Account:      *ctx.Caller.Account,
		ID:           *ctx.Caller.Account,
	})

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
			Tags:        tags,
			BaseScraper: config.BaseScraper,
			Config:      volume,
			Type:        "EBS",

			Name:    getName(tags, *volume.VolumeId),
			Account: *ctx.Caller.Account,
			ID:      *volume.VolumeId,
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
		*results = append(*results, v1.ScrapeResult{
			ExternalType: "AWS::RDS::DBInstance",
			Tags:         tags,
			BaseScraper:  config.BaseScraper,
			Config:       instance,
			Type:         "RDS",
			Name:         getName(tags, *instance.DBInstanceIdentifier),
			Account:      *ctx.Caller.Account,
			ID:           *instance.DBInstanceIdentifier,
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
		tags := getTags(vpc.Tags)
		*results = append(*results, v1.ScrapeResult{
			ExternalType: "AWS::EC2::VPC",
			Tags:         tags,
			BaseScraper:  config.BaseScraper,
			Config:       vpc,
			Type:         "VPC",
			Network:      *vpc.VpcId,
			Name:         getName(tags, *vpc.VpcId),
			Account:      *ctx.Caller.Account,
			ID:           *vpc.VpcId,
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

	for _, r := range describeOutput.Reservations {
		for _, i := range r.Instances {
			instance := NewInstance(i)
			*results = append(*results, v1.ScrapeResult{
				ExternalType: "AWS::EC2::Instance",
				Tags:         instance.Tags,
				BaseScraper:  config.BaseScraper,
				Config:       instance,
				Type:         "EC2Instance",
				Network:      instance.VpcID,
				Subnet:       instance.SubnetID,
				Zone:         ctx.Subnets[instance.SubnetID].Zone,
				Region:       ctx.Subnets[instance.SubnetID].Region,
				Name:         instance.GetHostname(),
				Account:      *ctx.Caller.Account,
				ID:           instance.InstanceID})
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
			ExternalType: "AWS::EC2::SecurityGroup",
			Tags:         tags,
			BaseScraper:  config.BaseScraper,
			Config:       sg,
			Type:         "SecurityGroup",
			Network:      *sg.VpcId,
			Name:         getName(tags, *sg.GroupId),
			Account:      *ctx.Caller.Account,
			ID:           *sg.GroupId})
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
			ExternalType: "AWS::EC2::RouteTable",
			Tags:         tags,
			BaseScraper:  config.BaseScraper,
			Config:       r,
			Type:         "Route",
			Network:      *r.VpcId,
			Name:         getName(tags, *r.RouteTableId),
			Account:      *ctx.Caller.Account,
			ID:           *r.RouteTableId})
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
			ExternalType: "AWS::EC2::DHCPOptions",
			Tags:         tags,
			BaseScraper:  config.BaseScraper,
			Config:       d,
			Type:         "DHCP",
			Name:         getName(tags, *d.DhcpOptionsId),
			Account:      *ctx.Caller.Account,
			ID:           *d.DhcpOptionsId})
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
			ExternalType: "AWS::EC2::Subnet",
			BaseScraper:  config.BaseScraper,
			Tags:         tags,
			Type:         "Subnet",
			ID:           *subnet.SubnetId,
			Subnet:       *subnet.SubnetId,
			Config:       subnet,
			Account:      *ctx.Caller.Account,
			Network:      *subnet.VpcId,
			Zone:         az,
			Region:       az[0 : len(az)-1],
		}
		*results = append(*results, result)
	}
}

// Scrape ...
func (aws Scraper) Scrape(ctx v1.ScrapeContext, config v1.ConfigScraper, _ v1.Manager) v1.ScrapeResults {
	results := &v1.ScrapeResults{}

	for _, awsConfig := range config.AWS {
		awsCtx, err := aws.getContext(ctx, awsConfig)
		if err != nil {
			return results.Errorf(err, "failed to create AWS context")
		}
		logger.Infof("Scrapping %s", awsCtx)

		aws.account(awsCtx, awsConfig, results)
		// aws.subnets(awsCtx, awsConfig, results)
		// aws.instances(awsCtx, awsConfig, results)
		// aws.vpcs(awsCtx, awsConfig, results)
		// aws.securityGroups(awsCtx, awsConfig, results)
		// aws.routes(awsCtx, awsConfig, results)
		// aws.dhcp(awsCtx, awsConfig, results)
		// aws.eksClusters(awsCtx, awsConfig, results)
		// aws.ebs(awsCtx, awsConfig, results)
		// aws.efs(awsCtx, awsConfig, results)
		// aws.rds(awsCtx, awsConfig, results)
		aws.config(awsCtx, awsConfig, results)
	}

	return *results
}
