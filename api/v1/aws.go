package v1

import (
	"strings"
	"time"
)

// AWS ...
type AWS struct {
	*AWSConnection
	PatchStates         bool       `json:"patch_states,omitempty"`
	PatchDetails        bool       `json:"patch_details,omitempty"`
	Inventory           bool       `json:"inventory,omitempty"`
	Compliance          bool       `json:"compliance,omitempty"`
	CloudTrail          CloudTrail `json:"cloudtrail,omitempty"`
	TrustedAdvisorCheck bool       `json:"trusted_advisor_check,omitempty"`
	Include             []string   `json:"include,omitempty"`
	Exclude             []string   `json:"exclude,omitempty"`
	BaseScraper         `json:",inline"`
	CostReporting       CostReporting `json:"cost_reporting,omitempty"`
}

type CloudTrail struct {
	Exclude []string       `json:"exclude,omitempty"`
	MaxAge  *time.Duration `json:"max_age,omitempty"`
}

type CostReporting struct {
	S3BucketPath string `json:"s3_bucket_path,omitempty"`
	Table        string `json:"table,omitempty"`
	Database     string `json:"database,omitempty"`
	Region       string `json:"region,omitempty"`
}

const (
	AWSEC2Instance    = "AWS::EC2::Instance"
	AWSEKSCluster     = "AWS::EKS::Cluster"
	AWSS3Bucket       = "AWS::S3::Bucket"
	AWSLoadBalancer   = "AWS::ElasticLoadBalancing::LoadBalancer"
	AWSLoadBalancerV2 = "AWS::ElasticLoadBalancingV2::LoadBalancer"
	AWSEBSVolume      = "AWS::EBS::Volume"
	AWSRDSInstance    = "AWS::RDS::DBInstance"
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
