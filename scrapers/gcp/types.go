package gcp

import (
	"strings"
)

// typeOverrides maps Google Cloud API resource types to their ServiceName::ResourceType format
var typeOverrides = map[string]string{
	// Instance resources (13 total)
	"alloydb.googleapis.com/Instance":             "AlloyDB::Instance",
	"bigtable.googleapis.com/Instance":            "Bigtable::Instance",
	"datafusion.googleapis.com/Instance":          "CloudDataFusion::Instance",
	"sqladmin.googleapis.com/Instance":            "SQLInstance",
	"compute.googleapis.com/Instance":             "Instance",
	"file.googleapis.com/Instance":                "Filestore::Instance",
	"looker.googleapis.com/Instance":              "Looker::Instance",
	"memcache.googleapis.com/Instance":            "MemorystoreMemcached::Instance",
	"redis.googleapis.com/Instance":               "MemorystoreRedis::Instance",
	"securesourcemanager.googleapis.com/Instance": "SecureSourceManager::Instance",
	"spanner.googleapis.com/Instance":             "Spanner::Instance",
	"notebooks.googleapis.com/Instance":           "VertexAIWorkbench::Instance",

	// AccessPolicy resources (2 total)
	"accesscontextmanager.googleapis.com/AccessPolicy": "AccessContextManager::AccessPolicy",

	// Agent resources (2 total)
	"dialogflow.googleapis.com/Agent": "Dialogflow::Agent",

	// Api resources (2 total)
	"apigateway.googleapis.com/Api": "APIGateway::Api",
	"apihub.googleapis.com/Api":     "APIHub::Api",

	// Application resources (2 total)
	"appengine.googleapis.com/Application": "AppEngine::Application",
	"apphub.googleapis.com/Application":    "AppHub::Application",

	// Asset resources (2 total)
	"dataplex.googleapis.com/Asset":   "Dataplex::Asset",
	"livestream.googleapis.com/Asset": "LiveStream::Asset",

	// BackupPlan resources (2 total)
	"backupdr.googleapis.com/BackupPlan":  "BackupDR::BackupPlan",
	"gkebackup.googleapis.com/BackupPlan": "GKEBackup::BackupPlan",

	// BackupVault resources (2 total)
	"backupdr.googleapis.com/BackupVault": "BackupDR::BackupVault",
	"netapp.googleapis.com/BackupVault":   "NetAppVolumes::BackupVault",

	// Channel resources (2 total)
	"eventarc.googleapis.com/Channel":   "Eventarc::Channel",
	"livestream.googleapis.com/Channel": "LiveStream::Channel",

	// ConnectionProfile resources (2 total)
	"datamigration.googleapis.com/ConnectionProfile": "DatabaseMigration::ConnectionProfile",
	"datastream.googleapis.com/ConnectionProfile":    "Datastream::ConnectionProfile",

	// Feature resources (2 total)
	"gkehub.googleapis.com/Feature":     "GKEHub::Feature",
	"aiplatform.googleapis.com/Feature": "VertexAI::Feature",

	// Folder resources (2 total)
	"cloudresourcemanager.googleapis.com/Folder": "ResourceManager::Folder",

	// Gateway resources (2 total)
	"apigateway.googleapis.com/Gateway":      "APIGateway::Gateway",
	"networkservices.googleapis.com/Gateway": "NetworkServices::Gateway",

	// Group resources (2 total)
	"monitoring.googleapis.com/Group":  "CloudMonitoring::Group",
	"vmmigration.googleapis.com/Group": "MigrateVMs::Group",

	// Image resources (2 total)
	"compute.googleapis.com/Image":           "Compute::Image",
	"containerregistry.googleapis.com/Image": "ContainerRegistry::Image",

	// Intent resources (2 total)
	"dialogflow.googleapis.com/Intent": "Dialogflow::Intent",

	// Key resources (2 total)
	"apikeys.googleapis.com/Key":             "APIKeys::Key",
	"recaptchaenterprise.googleapis.com/Key": "reCAPTCHA::Key",

	// MembershipBinding resources (2 total)
	"servicemesh.googleapis.com/MembershipBinding": "CloudServiceMesh::MembershipBinding",
	"gkehub.googleapis.com/MembershipBinding":      "GKEHub::MembershipBinding",

	// Model resources (2 total)
	"bigquery.googleapis.com/Model":   "BigQuery::Model",
	"aiplatform.googleapis.com/Model": "VertexAI::Model",

	// Namespace resources (2 total)
	"gkehub.googleapis.com/Namespace":           "GKEHub::Namespace",
	"servicedirectory.googleapis.com/Namespace": "ServiceDirectory::Namespace",

	// SessionEntityType resources (2 total)
	"dialogflow.googleapis.com/SessionEntityType": "Dialogflow::SessionEntityType",

	// Source resources (2 total)
	"vmmigration.googleapis.com/Source":    "MigrateVMs::Source",
	"securitycenter.googleapis.com/Source": "SecurityCenter::Source",

	// Table resources (2 total)
	"bigquery.googleapis.com/Table": "BigQuery::Table",
	"bigtable.googleapis.com/Table": "Bigtable::Table",

	// Topic resources (2 total)
	"managedkafka.googleapis.com/Topic": "ManagedKafka::Topic",
	"pubsub.googleapis.com/Topic":       "PubSub::Topic",

	// Zone resources (2 total)
	"dataplex.googleapis.com/Zone":              "Dataplex::Zone",
	"gdchardwaremanagement.googleapis.com/Zone": "GDCHardwareManagement::Zone",

	// Connection resources (3 total)
	"developerconnect.googleapis.com/Connection":  "DeveloperConnect::Connection",
	"connectors.googleapis.com/Connection":        "IntegrationConnectors::Connection",
	"servicenetworking.googleapis.com/Connection": "ServiceNetworking::Connection",

	// Database resources (3 total)
	"sqladmin.googleapis.com/Database":  "CloudSQL::Database",
	"firestore.googleapis.com/Database": "Firestore::Database",
	"spanner.googleapis.com/Database":   "Spanner::Database",

	// Dataset resources (3 total)
	"bigquery.googleapis.com/Dataset":   "BigQuery::Dataset",
	"healthcare.googleapis.com/Dataset": "CloudHealthcare::Dataset",
	"aiplatform.googleapis.com/Dataset": "VertexAI::Dataset",

	// Endpoint resources (3 total)
	"ids.googleapis.com/Endpoint":              "CloudIDS::Endpoint",
	"servicedirectory.googleapis.com/Endpoint": "ServiceDirectory::Endpoint",
	"aiplatform.googleapis.com/Endpoint":       "VertexAI::Endpoint",

	// EntityType resources (3 total)
	"dialogflow.googleapis.com/EntityType": "Dialogflow::EntityType",
	"aiplatform.googleapis.com/EntityType": "VertexAI::EntityType",

	// Environment resources (3 total)
	"apigee.googleapis.com/Environment":    "Apigee::Environment",
	"composer.googleapis.com/Environment":  "CloudComposer::Environment",
	"notebooks.googleapis.com/Environment": "VertexAIWorkbench::Environment",

	// Organization resources (3 total)
	"apigee.googleapis.com/Organization":               "Apigee::Organization",
	"cloudresourcemanager.googleapis.com/Organization": "ResourceManager::Organization",

	// Policy resources (3 total)
	"dns.googleapis.com/Policy":                  "CloudDNS::Policy",
	"cloudresourcemanager.googleapis.com/Policy": "ResourceManager::Policy",
	"orgpolicy.googleapis.com/Policy":            "OrganizationPolicy::Policy",

	// PrivateConnection resources (3 total)
	"datamigration.googleapis.com/PrivateConnection": "DatabaseMigration::PrivateConnection",
	"datastream.googleapis.com/PrivateConnection":    "Datastream::PrivateConnection",
	"vmwareengine.googleapis.com/PrivateConnection":  "VMwareEngine::PrivateConnection",

	// Project resources (3 total)
	"compute.googleapis.com/Project":              "Compute::Project",
	"cloudresourcemanager.googleapis.com/Project": "ResourceManager::Project",

	// Repository resources (3 total)
	"artifactregistry.googleapis.com/Repository":    "ArtifactRegistry::Repository",
	"dataform.googleapis.com/Repository":            "Dataform::Repository",
	"securesourcemanager.googleapis.com/Repository": "SecureSourceManager::Repository",

	// Workload resources (3 total)
	"apphub.googleapis.com/Workload":               "AppHub::Workload",
	"assuredworkloads.googleapis.com/Workload":     "AssuredWorkloads::Workload",
	"cloudcontrolspartner.googleapis.com/Workload": "CloudControlsPartner::Workload",

	// Snapshot resources (4 total)
	"compute.googleapis.com/Snapshot": "Compute::Snapshot",
	"file.googleapis.com/Snapshot":    "Filestore::Snapshot",
	"netapp.googleapis.com/Snapshot":  "NetAppVolumes::Snapshot",
	"pubsub.googleapis.com/Snapshot":  "PubSub::Snapshot",

	// Backup resources (6 total)
	"alloydb.googleapis.com/Backup":           "AlloyDB::Backup",
	"bigtable.googleapis.com/Backup":          "Bigtable::Backup",
	"metastore.googleapis.com/Backup":         "DataprocMetastore::Backup",
	"file.googleapis.com/Backup":              "Filestore::Backup",
	"managedidentities.googleapis.com/Backup": "ManagedActiveDirectory::Backup",
	"spanner.googleapis.com/Backup":           "Spanner::Backup",

	// Cluster resources (6 total)
	"alloydb.googleapis.com/Cluster":      "AlloyDB::Cluster",
	"bigtable.googleapis.com/Cluster":     "Bigtable::Cluster",
	"dataproc.googleapis.com/Cluster":     "Dataproc::Cluster",
	"managedkafka.googleapis.com/Cluster": "ManagedKafka::Cluster",
	"vmwareengine.googleapis.com/Cluster": "VMwareEngine::Cluster",
	"container.googleapis.com/Cluster":    "GKECluster",

	// Job resources (6 total)
	"batch.googleapis.com/Job":      "Batch::Job",
	"bigquery.googleapis.com/Job":   "BigQuery::Job",
	"run.googleapis.com/Job":        "CloudRun::Job",
	"dataflow.googleapis.com/Job":   "Dataflow::Job",
	"dataproc.googleapis.com/Job":   "Dataproc::Job",
	"transcoder.googleapis.com/Job": "Transcoder::Job",

	// Service resources (8 total)
	"appengine.googleapis.com/Service":         "AppEngine::Service",
	"apphub.googleapis.com/Service":            "AppHub::Service",
	"monitoring.googleapis.com/Service":        "CloudMonitoring::Service",
	"run.googleapis.com/Service":               "CloudRun::Service",
	"metastore.googleapis.com/Service":         "DataprocMetastore::Service",
	"servicedirectory.googleapis.com/Service":  "ServiceDirectory::Service",
	"servicemanagement.googleapis.com/Service": "ServiceManagement::Service",
	"serviceusage.googleapis.com/Service":      "ServiceUsage::Service",
}

func parseGCPConfigClass(assetType string) string {
	parts := strings.Split(assetType, ".googleapis.com/")
	if len(parts) != 2 {
		return "GCP::" + assetType
	}

	if typ, ok := typeOverrides[assetType]; ok {
		return typ
	}
	// compute.googleapis.com/InstanceSettings => InstanceSettings
	return parts[1]
}
