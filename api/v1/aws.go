package v1

import (
	"strings"
	"time"

	"github.com/flanksource/commons/logger"
)

// AWS ...
type AWS struct {
	BaseScraper   `json:",inline"`
	AWSConnection `json:",inline"`
	Compliance    bool          `json:"compliance,omitempty"`
	CloudTrail    CloudTrail    `json:"cloudtrail,omitempty"`
	Include       []string      `json:"include,omitempty"`
	Exclude       []string      `json:"exclude,omitempty"`
	CostReporting CostReporting `json:"costReporting,omitempty"`
}

type CloudTrail struct {
	Exclude []string `json:"exclude,omitempty"`
	MaxAge  string   `json:"maxAge,omitempty"`
}

func (c CloudTrail) GetMaxAge() time.Duration {
	if c.MaxAge == "" {
		return 7 * 24 * time.Hour
	}
	d, err := time.ParseDuration(c.MaxAge)
	if err != nil {
		logger.Warnf("Invalid cloudtrail max age %s: %v", c.MaxAge, err)
		return 7 * 24 * time.Hour
	}
	return d
}

type CostReporting struct {
	S3BucketPath string `json:"s3BucketPath,omitempty"`
	Table        string `json:"table,omitempty"`
	Database     string `json:"database,omitempty"`
	Region       string `json:"region,omitempty"`
}

const (
	AWSECSCluster         = "AWS::ECS::Cluster"
	AWSECSService         = "AWS::EC2::Service"
	AWSECSTask            = "AWS::ECS:Task"
	AWSEKSFargateProfile  = "AWS::ECS::FargateProfile"
	AWSElastiCacheCluster = "AWS::ElastiCache::CacheCluster"
	AWSLambdaFunction     = "AWS::Lambda::Function"
	AWSSNSTopic           = "AWS::SNS::Topic"
	AWSSQS                = "AWS::SQS::Queue"
	AWSRegion             = "AWS::Region"
	AWSZone               = "AWS::Route53::HostedZone"
	AWSEC2Instance        = "AWS::EC2::Instance"
	AWSEKSCluster         = "AWS::EKS::Cluster"
	AWSS3Bucket           = "AWS::S3::Bucket"
	AWSLoadBalancer       = "AWS::ElasticLoadBalancing::LoadBalancer"
	AWSLoadBalancerV2     = "AWS::ElasticLoadBalancingV2::LoadBalancer"
	AWSEBSVolume          = "AWS::EBS::Volume"
	AWSRDSInstance        = "AWS::RDS::DBInstance"
	AWSEC2VPC             = "AWS::EC2::VPC"
	AWSEC2Subnet          = "AWS::EC2::Subnet"
	AWSAccount            = "AWS::::Account"
	AWSAvailabilityZone   = "AWS::AvailabilityZone"
	AWSAvailabilityZoneID = "AWS::AvailabilityZoneID"
	AWSEC2SecurityGroup   = "AWS::EC2::SecurityGroup"
	AWSIAMUser            = "AWS::IAM::User"
	AWSIAMRole            = "AWS::IAM::Role"
	AWSIAMInstanceProfile = "AWS::IAM::InstanceProfile"
	AWSEC2AMI             = "AWS::EC2::AMI"
	AWSEC2DHCPOptions     = "AWS::EC2::DHCPOptions"
)

func (aws AWS) Includes(resource string) bool {
	if len(aws.Include) == 0 {
		return true
	}
	for _, include := range aws.Include {
		if strings.EqualFold(include, resource) {
			return true
		}
	}
	return false
}

func (aws AWS) Excludes(resource string) bool {
	if len(aws.Exclude) == 0 {
		return false
	}
	for _, exclude := range aws.Exclude {
		if strings.EqualFold(exclude, resource) {
			return true
		}
	}
	return false
}
