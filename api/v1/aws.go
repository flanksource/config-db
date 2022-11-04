package v1

import (
	"strings"
	"time"

	"github.com/flanksource/commons/logger"
)

// AWS ...
type AWS struct {
	BaseScraper `json:",inline"`
	*AWSConnection
	PatchStates         bool          `json:"patch_states,omitempty"`
	PatchDetails        bool          `json:"patch_details,omitempty"`
	Inventory           bool          `json:"inventory,omitempty"`
	Compliance          bool          `json:"compliance,omitempty"`
	CloudTrail          CloudTrail    `json:"cloudtrail,omitempty"`
	TrustedAdvisorCheck bool          `json:"trusted_advisor_check,omitempty"`
	Include             []string      `json:"include,omitempty"`
	Exclude             []string      `json:"exclude,omitempty"`
	CostReporting       CostReporting `json:"cost_reporting,omitempty"`
}

type CloudTrail struct {
	Exclude []string `json:"exclude,omitempty"`
	MaxAge  string   `json:"max_age,omitempty"`
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
	S3BucketPath string `json:"s3_bucket_path,omitempty"`
	Table        string `json:"table,omitempty"`
	Database     string `json:"database,omitempty"`
	Region       string `json:"region,omitempty"`
}

const (
	AWSEC2Instance      = "AWS::EC2::Instance"
	AWSEKSCluster       = "AWS::EKS::Cluster"
	AWSS3Bucket         = "AWS::S3::Bucket"
	AWSLoadBalancer     = "AWS::ElasticLoadBalancing::LoadBalancer"
	AWSLoadBalancerV2   = "AWS::ElasticLoadBalancingV2::LoadBalancer"
	AWSEBSVolume        = "AWS::EBS::Volume"
	AWSRDSInstance      = "AWS::RDS::DBInstance"
	AWSEC2VPC           = "AWS::EC2::VPC"
	AWSEC2Subnet        = "AWS::EC2::Subnet"
	AWSAccount          = "AWS::::Account"
	AWSEC2SecurityGroup = "AWS::EC2::SecurityGroup"
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
