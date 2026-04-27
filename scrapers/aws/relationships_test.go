package aws

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAWSRDSSecurityGroupRelationshipsDirection(t *testing.T) {
	rels := awsRDSSecurityGroupRelationships("db-1", []string{"sg-1"})
	require.Len(t, rels, 1)

	assert.Equal(t, "sg-1", rels[0].ConfigExternalID.ExternalID)
	assert.Equal(t, v1.AWSEC2SecurityGroup, rels[0].ConfigExternalID.ConfigType)
	assert.Equal(t, "db-1", rels[0].RelatedExternalID.ExternalID)
	assert.Equal(t, v1.AWSRDSInstance, rels[0].RelatedExternalID.ConfigType)
	assert.Equal(t, "RDSSecurityGroup", rels[0].Relationship)
}

func TestAWSEC2NetworkRelationshipsRemainParentToChild(t *testing.T) {
	rels := awsEC2NetworkRelationships(
		v1.ExternalID{ExternalID: "i-1", ConfigType: v1.AWSEC2Instance},
		[]string{"sg-1"},
		"subnet-1",
	)
	require.Len(t, rels, 2)

	assert.Equal(t, "sg-1", rels[0].ConfigExternalID.ExternalID)
	assert.Equal(t, v1.AWSEC2SecurityGroup, rels[0].ConfigExternalID.ConfigType)
	assert.Equal(t, "i-1", rels[0].RelatedExternalID.ExternalID)
	assert.Equal(t, v1.AWSEC2Instance, rels[0].RelatedExternalID.ConfigType)
	assert.Equal(t, "SecurityGroupInstance", rels[0].Relationship)

	assert.Equal(t, "subnet-1", rels[1].ConfigExternalID.ExternalID)
	assert.Equal(t, v1.AWSEC2Subnet, rels[1].ConfigExternalID.ConfigType)
	assert.Equal(t, "i-1", rels[1].RelatedExternalID.ExternalID)
	assert.Equal(t, v1.AWSEC2Instance, rels[1].RelatedExternalID.ConfigType)
	assert.Equal(t, "SubnetInstance", rels[1].Relationship)
}

func TestAWSEKSClusterRelationshipsUseClusterARN(t *testing.T) {
	clusterARN := "arn:aws:eks:us-east-1:123456789012:cluster/demo"
	roleARN := "arn:aws:iam::123456789012:role/demo"

	rels := awsEKSClusterRelationships(clusterARN, roleARN, []string{"subnet-1"}, "sg-1")
	require.Len(t, rels, 3)

	assert.Equal(t, roleARN, rels[0].ConfigExternalID.ExternalID)
	assert.Equal(t, v1.AWSIAMRole, rels[0].ConfigExternalID.ConfigType)
	assert.Equal(t, clusterARN, rels[0].RelatedExternalID.ExternalID)
	assert.Equal(t, v1.AWSEKSCluster, rels[0].RelatedExternalID.ConfigType)
	assert.Equal(t, "EKSIAMRole", rels[0].Relationship)

	assert.Equal(t, "subnet-1", rels[1].ConfigExternalID.ExternalID)
	assert.Equal(t, v1.AWSEC2Subnet, rels[1].ConfigExternalID.ConfigType)
	assert.Equal(t, clusterARN, rels[1].RelatedExternalID.ExternalID)
	assert.Equal(t, "SubnetEKS", rels[1].Relationship)

	assert.Equal(t, "sg-1", rels[2].ConfigExternalID.ExternalID)
	assert.Equal(t, v1.AWSEC2SecurityGroup, rels[2].ConfigExternalID.ConfigType)
	assert.Equal(t, clusterARN, rels[2].RelatedExternalID.ExternalID)
	assert.Equal(t, "EKSSecuritygroups", rels[2].Relationship)
}

func TestAWSClassicLoadBalancerInstanceRelationshipsDirection(t *testing.T) {
	rels := awsClassicLoadBalancerInstanceRelationships("arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/lb-1", []string{"i-1"})
	require.Len(t, rels, 1)

	assert.Equal(t, "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/lb-1", rels[0].ConfigExternalID.ExternalID)
	assert.Equal(t, v1.AWSLoadBalancer, rels[0].ConfigExternalID.ConfigType)
	assert.Equal(t, "i-1", rels[0].RelatedExternalID.ExternalID)
	assert.Equal(t, v1.AWSEC2Instance, rels[0].RelatedExternalID.ConfigType)
	assert.Equal(t, "LoadBalancerInstance", rels[0].Relationship)
}

func TestAWSEKSLoadBalancerRelationshipDirection(t *testing.T) {
	clusterARN := "arn:aws:eks:us-east-1:123456789012:cluster/demo"
	loadBalancerExternalID := v1.ExternalID{
		ExternalID: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/demo/123",
		ConfigType: v1.AWSLoadBalancerV2,
	}

	rel := awsEKSLoadBalancerRelationship(clusterARN, loadBalancerExternalID)

	assert.Equal(t, clusterARN, rel.ConfigExternalID.ExternalID)
	assert.Equal(t, v1.AWSEKSCluster, rel.ConfigExternalID.ConfigType)
	assert.Equal(t, loadBalancerExternalID.ExternalID, rel.RelatedExternalID.ExternalID)
	assert.Equal(t, v1.AWSLoadBalancerV2, rel.RelatedExternalID.ConfigType)
	assert.Equal(t, "EKSLoadBalancer", rel.Relationship)
}
