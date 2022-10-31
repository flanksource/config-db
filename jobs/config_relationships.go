package jobs

import (
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
)

func SyncConfigRelationships() {
	// Fetch all items of specific type, link them
	// parent -> ec2, child -> security-groups
	// eks -> ec2
	// elb -> ec2
	syncAWSInstanceRelationships()
	syncAWSLoadBalancerRelationships()
}

func syncAWSInstanceRelationships() {
	configItems, err := db.FindConfigItemFromType(v1.AWSEC2Instance)
	if err != nil {
		logger.Errorf("Error fetching ec2 instance config items: %v", err)
		return
	}

	var relationships []models.ConfigRelationship
	for _, ci := range configItems {
		config, err := ci.ConfigJSONStringMap()
		if err != nil {
			logger.Errorf("Error getting config item json: %v", err)
		}
		sgs := config["securityGroups"].(map[string]interface{})
		for sgID := range sgs {
			sgDBID, err := db.FindConfigItemID(models.CIExternalUID{
				ExternalType: v1.AWSEC2SecurityGroup,
				ExternalID:   []string{sgID},
			})
			if err != nil {
				logger.Errorf("Error fetching config item: %v", err)
			}

			relationships = append(relationships, models.ConfigRelationship{
				ParentID: ci.ID,
				ChildID:  *sgDBID,
				Relation: "InstanceSecurityGroup",
			})
		}
	}
}

func syncAWSLoadBalancerRelationships() {
	configItems, err := db.FindConfigItemFromType(v1.AWSLoadBalancer)
	if err != nil {
		logger.Errorf("Error fetching ec2 instance config items: %v", err)
		return
	}

	var relationships []models.ConfigRelationship
	for _, ci := range configItems {
		config, err := ci.ConfigJSONStringMap()
		if err != nil {
			logger.Errorf("Error getting config item json: %v", err)
		}
		instances := config["instances"].([]map[string]interface{})
		for _, instance := range instances {
			instanceID := instance["id"].(string)
			instanceDBID, err := db.FindConfigItemID(models.CIExternalUID{
				ExternalType: v1.AWSEC2Instance,
				ExternalID:   []string{instanceID},
			})
			if err != nil {
				logger.Errorf("Error fetching config item: %v", err)
			}

			relationships = append(relationships, models.ConfigRelationship{
				ParentID: ci.ID,
				ChildID:  *instanceDBID,
				Relation: "ClusterNode",
			})
		}
	}
}
