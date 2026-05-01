package v1

import (
	"strings"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/samber/lo"
)

// AWS ...
type AWS struct {
	BaseScraper   `yaml:",inline" json:",inline"`
	AWSConnection `yaml:",inline" json:",inline"`

	Compliance    bool          `json:"compliance,omitempty"`
	CloudTrail    CloudTrail    `json:"cloudtrail,omitempty"`
	Include       []string      `json:"include,omitempty"`
	Exclude       []string      `json:"exclude,omitempty"`
	Exclusions    AWSExclusions `json:"exclusions,omitempty"`
	CostReporting CostReporting `json:"costReporting,omitempty"`
}

// AWSExclusion matches a scraped item by type, name, and/or tags.
// Empty fields are wildcards. Type and Name accept comma-separated patterns
// and are matched via collections.MatchItems, so glob (`*`, `prefix*`,
// `*suffix*`) and negation (`!pattern`) are supported. A rule matches when
// ALL populated fields match (AND).
type AWSExclusion struct {
	Type string            `json:"type,omitempty" yaml:"type,omitempty"`
	Name string            `json:"name,omitempty" yaml:"name,omitempty"`
	Tags map[string]string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// splitPatterns splits a comma-separated pattern list into trimmed, non-empty
// pieces. A single pattern with no comma yields a single-element slice.
func splitPatterns(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// AWSExclusions is an OR-combined list of AWSExclusion rules. An item is
// excluded when ANY rule matches.
type AWSExclusions []AWSExclusion

// Matches reports whether any rule excludes the given item. tags may be nil
// when the SDK List* call does not return tags; in that case rules with a
// populated Tags field simply don't match (they are not treated as a
// wildcard hit), so IAM/ELB resources can still be filtered on name+type
// without an extra Describe/ListTags call.
func (ex AWSExclusions) Matches(configType, name string, tags map[string]string) bool {
	for _, rule := range ex {
		if rule.matches(configType, name, tags) {
			return true
		}
	}
	return false
}

func (r AWSExclusion) matches(configType, name string, tags map[string]string) bool {
	if r.Type == "" && r.Name == "" && len(r.Tags) == 0 {
		return false
	}
	if r.Type != "" && !collections.MatchItems(configType, splitPatterns(r.Type)...) {
		return false
	}
	if r.Name != "" && !collections.MatchItems(name, splitPatterns(r.Name)...) {
		return false
	}
	if len(r.Tags) > 0 {
		if tags == nil {
			return false
		}
		for k, pattern := range r.Tags {
			v, ok := tags[k]
			if !ok {
				return false
			}
			if !collections.MatchItems(v, pattern) {
				return false
			}
		}
	}
	return true
}

// ShouldExclude is a convenience wrapper over Exclusions.Matches.
func (aws AWS) ShouldExclude(configType, name string, tags map[string]string) bool {
	return aws.Exclusions.Matches(configType, name, tags)
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
	AWSCloudFormationStack = "AWS::CloudFormation::Stack"
	AWSECSCluster          = "AWS::ECS::Cluster"
	AWSECSService          = "AWS::ECS::Service"
	AWSECSTaskDefinition   = "AWS::ECS::TaskDefinition"
	AWSECSTask             = "AWS::ECS::Task"
	AWSEKSFargateProfile   = "AWS::EKS::FargateProfile"
	AWSElastiCacheCluster  = "AWS::ElastiCache::CacheCluster"
	AWSLambdaFunction      = "AWS::Lambda::Function"
	AWSSNSTopic            = "AWS::SNS::Topic"
	AWSSQS                 = "AWS::SQS::Queue"
	AWSRegion              = "AWS::Region"
	AWSZone                = "AWS::Route53::HostedZone"
	AWSEC2Instance         = "AWS::EC2::Instance"
	AWSEKSCluster          = "AWS::EKS::Cluster"
	AWSS3Bucket            = "AWS::S3::Bucket"
	AWSLoadBalancer        = "AWS::ElasticLoadBalancing::LoadBalancer"
	AWSLoadBalancerV2      = "AWS::ElasticLoadBalancingV2::LoadBalancer"
	AWSEBSVolume           = "AWS::EBS::Volume"
	AWSRDSInstance         = "AWS::RDS::DBInstance"
	AWSEC2VPC              = "AWS::EC2::VPC"
	AWSEC2Subnet           = "AWS::EC2::Subnet"
	AWSAccount             = "AWS::::Account"
	AWSAvailabilityZone    = "AWS::AvailabilityZone"
	AWSAvailabilityZoneID  = "AWS::AvailabilityZoneID"
	AWSEC2SecurityGroup    = "AWS::EC2::SecurityGroup"
	AWSIAMUser             = "AWS::IAM::User"
	AWSIAMRole             = "AWS::IAM::Role"
	AWSIAMGroup            = "AWS::IAM::Group"
	AWSIAMInstanceProfile  = "AWS::IAM::InstanceProfile"
	AWSIAMOIDCProvider     = "AWS::IAM::OIDCProvider"
	AWSIAMSAMLProvider     = "AWS::IAM::SAMLProvider"
	AWSEC2AMI              = "AWS::EC2::AMI"
	AWSEC2DHCPOptions      = "AWS::EC2::DHCPOptions"
	AWSBackupVault         = "AWS::Backup::BackupVault"
	AWSBackupPlan          = "AWS::Backup::BackupPlan"
	AWSEFSFileSystem       = "AWS::EFS::FileSystem"
	AWSDynamoDBTable       = "AWS::DynamoDB::Table"
)

var defaultAWSExclusions = []string{"ECSTaskDefinition"}

func (aws AWS) Includes(resource string) bool {
	if len(aws.Include) == 0 {
		return !lo.ContainsBy(defaultAWSExclusions, func(item string) bool {
			return strings.EqualFold(item, resource)
		})
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
		return lo.ContainsBy(defaultAWSExclusions, func(item string) bool {
			return strings.EqualFold(item, resource)
		})
	}

	for _, exclude := range aws.Exclude {
		if strings.EqualFold(exclude, resource) {
			return true
		}
	}

	return false
}
