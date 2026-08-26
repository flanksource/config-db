package v1

import (
	"fmt"
	"slices"
	"strings"

	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/types"
)

const (
	GCSBucket         = "GCP::Bucket"
	RedisInstance     = "GCP::Redis"
	MemcacheInstance  = "GCP::MemCache"
	PubSubTopic       = "GCP::PubSub"
	CloudSQLInstance  = "GCP::SQLInstance"
	IAMRole           = "GCP::IAMRole"
	IAMServiceAccount = "GCP::ServiceAccount"

	GCPInstance   = "GCP::Instance"
	GCPSubnet     = "GCP::Subnetwork"
	GCPNetwork    = "GCP::Network"
	GCPDisk       = "GCP::Disk"
	GCPGKECluster = "GCP::GKECluster"

	GCPManagedZone       = "GCP::ManagedZone"
	GCPResourceRecordSet = "GCP::ResourceRecordSet"

	GCPBackup    = "GCP::Backup"
	GCPBackupRun = "GCP::BackupRun"

	GCPProject      = "GCP::Project"
	GCPOrganization = "GCP::Organization"
)

const (
	// Feature flags for GCP scraper
	IncludeIAMPolicy    = "IAMPolicy"
	IncludeAuditLogs    = "AuditLogs"
	IncludeGroupMembers = "IAMGroupMembers"

	ExcludeSecurityCenter = "SecurityCenter"
)

var (
	AllIncludes = []string{IncludeIAMPolicy, IncludeAuditLogs, IncludeGroupMembers}
)

type GCP struct {
	BaseScraper              `json:",inline"`
	connection.GCPConnection `json:",inline"`

	// Organization to scrape, given as an organization number ("1234567890") or a
	// qualified name ("organizations/1234567890"). Its resource hierarchy and
	// Security Center findings are scraped at the organization root unless projects
	// narrows the scrape to selected project roots.
	// Projects that belong to no organization can still be scraped by listing
	// them in projects without an organization.
	Organization string `json:"organization,omitempty"`

	// Projects narrows the scrape to these projects, given as project ids
	// ("gcp-proj-1") or qualified names ("projects/gcp-proj-1"). Empty means every
	// project in the organization. Combined with an organization, only projects
	// that actually belong to it are scraped.
	Projects []string `json:"projects,omitempty"`

	// Project is an alias for a single-entry projects list.
	Project string `json:"project,omitempty"`

	// Include holds GCP asset types and/or feature flags, and is a strict
	// allowlist: leave it empty and everything except AuditLogs runs, but set it
	// and only what is listed runs. Anything omitted is silently off, so narrowing
	// one dimension turns the other off entirely:
	//
	//   include: [storage.googleapis.com/Bucket]   # buckets only, NO IAM/RBAC
	//   include: [IAMPolicy]                       # IAM/RBAC only, NO assets
	//
	// List both to filter assets while keeping the rest:
	//
	//   include: [storage.googleapis.com/Bucket, IAMPolicy, IAMGroupMembers]
	//
	// Asset types reference: https://cloud.google.com/asset-inventory/docs/supported-asset-types
	//
	// Feature flags:
	//   IAMPolicy       - RBAC access from IAM policy bindings, and the resource
	//                     hierarchy (organization and folder config items), which
	//                     is read as part of the same pass.
	//   IAMGroupMembers - expand Google group membership via the Cloud Identity
	//                     groups.readonly scope. Disable with exclude: [IAMGroupMembers].
	//   AuditLogs       - BigQuery audit-log access. Opt-in: it runs only when
	//                     listed here explicitly.
	Include []string `json:"include,omitempty"`

	// Exclude is a list of GCP asset types to exclude from scraping.
	Exclude []string `json:"exclude,omitempty"`

	// AuditLogs query the BigQuery dataset for audit logs.
	AuditLogs GCPAuditLogs `json:"auditLogs,omitempty"`

	// CostReporting reads the Cloud Billing export from BigQuery.
	CostReporting GCPCostReporting `json:"costReporting,omitempty"`
}

// GCPCostReporting locates the Cloud Billing export table.
//
// This must be the *detailed* usage cost export
// (gcp_billing_export_resource_v1_<BILLING_ACCOUNT_ID>). The standard export carries no
// resource column at all, so every charge would be attributed to its project rather than
// to the resource that incurred it.
type GCPCostReporting struct {
	// Project holding the billing export dataset. Defaults to the scraped project.
	Project string `json:"project,omitempty"`

	// Dataset holding the export table, e.g. "billing_export".
	Dataset string `json:"dataset,omitempty"`

	// Table is the export table, e.g.
	// "gcp_billing_export_resource_v1_01ABCD_2345EF_67890A".
	Table string `json:"table,omitempty"`

	// LookbackDays controls the UTC-midnight lower bound for each export read.
	// Values <= 0 use the 45-day default; the current partial UTC day is included.
	// BigQuery bills by bytes scanned, so this is the main cost control.
	LookbackDays int `json:"lookbackDays,omitempty"`
}

// IsEmpty reports whether cost reporting was left unconfigured.
func (c GCPCostReporting) IsEmpty() bool {
	return c.Project == "" && c.Dataset == "" && c.Table == "" && c.LookbackDays == 0
}

// Validate reports whether the export table can be located.
func (c GCPCostReporting) Validate() error {
	if c.IsEmpty() {
		return nil
	}
	var missing []string
	if c.Dataset == "" {
		missing = append(missing, "dataset")
	}
	if c.Table == "" {
		missing = append(missing, "table")
	}
	if len(missing) > 0 {
		return fmt.Errorf("incomplete GCP costReporting: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

type GCPAuditLogs struct {
	// BigQuery dataset to query audit logs from
	// Example: "default._AllLogs"
	Dataset string `json:"dataset,omitempty"`

	// Project holding the BigQuery dataset. Defaults to the scraped project.
	// An organization-scoped scrape must set this to the project its aggregated
	// log sink writes to, since the organization itself holds no dataset.
	Project string `json:"project,omitempty"`

	// Time range to query audit logs (defaults to last 7 days if not specified)
	// Examples: "24h", "7d", "30d"
	Since string `json:"since,omitempty"`

	// Filter user agents matching these patterns
	UserAgents types.MatchExpressions `json:"userAgents,omitempty"`

	// Filter principal emails matching these patterns
	PrincipalEmails types.MatchExpressions `json:"principalEmails,omitempty"`

	// Filter permissions matching these patterns
	Permissions types.MatchExpressions `json:"permissions,omitempty"`

	// Filter service names matching these patterns
	ServiceNames types.MatchExpressions `json:"serviceNames,omitempty"`

	// Filter methods matching these patterns
	Methods types.MatchExpressions `json:"methods,omitempty"`
}

const (
	ProjectPrefix      = "projects/"
	OrganizationPrefix = "organizations/"
)

// Validate reports whether the config names anything to scrape.
func (gcp GCP) Validate() error {
	if gcp.Organization == "" && len(gcp.ConfiguredProjects()) == 0 {
		return fmt.Errorf("one of organization or projects must be set")
	}
	return nil
}

// Scope describes what the scrape covers, for logs and error messages.
func (gcp GCP) Scope() string {
	if gcp.IsOrgScoped() {
		return OrganizationPrefix + gcp.OrganizationID()
	}
	return strings.Join(gcp.ConfiguredProjects(), ", ")
}

// IsOrgScoped reports whether an organization was configured. Its hierarchy is
// then available in addition to the work performed at the resolved scrape roots.
func (gcp GCP) IsOrgScoped() bool {
	return gcp.Organization != ""
}

// OrganizationID returns the bare organization number, without the
// organizations/ prefix. Empty when no organization is configured.
func (gcp GCP) OrganizationID() string {
	return strings.TrimPrefix(gcp.Organization, OrganizationPrefix)
}

// ConfiguredProjects returns the explicitly named projects as bare project ids,
// merging the singular project alias. Empty means the scrape is not narrowed to
// a subset, i.e. every project in the organization.
func (gcp GCP) ConfiguredProjects() []string {
	var projects []string
	seen := make(map[string]struct{})

	for _, project := range append(slices.Clone(gcp.Projects), gcp.Project) {
		id := strings.TrimPrefix(project, ProjectPrefix)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		projects = append(projects, id)
	}

	return projects
}

func (gcp GCP) Includes(resource string) bool {
	if len(gcp.Include) == 0 {
		return true
	}
	for _, include := range gcp.Include {
		if strings.EqualFold(include, resource) {
			return true
		}
	}
	return false
}

func (gcp GCP) Excludes(resource string) bool {
	if len(gcp.Exclude) == 0 {
		return false
	}
	for _, exclude := range gcp.Exclude {
		if strings.EqualFold(exclude, resource) {
			return true
		}
	}
	return false
}

// ProjectAssetType is the Resource Manager project. It is the config item every other
// asset hangs its parent edge off and the root unattributed spend is booked against, so a
// narrowed scrape still has to produce it.
const ProjectAssetType = "cloudresourcemanager.googleapis.com/Project"

// GetAssetTypes returns the asset types to scrape from Include field.
//
// A narrowed list always gains the project: the hierarchy pass deliberately does not emit
// one — it links to the item the asset pass produces — so leaving it out means nothing
// creates it and every parent edge and cost root dangles.
func (gcp GCP) GetAssetTypes() []string {
	var assetTypes []string
	for _, include := range gcp.Include {
		if !slices.Contains(AllIncludes, include) {
			assetTypes = append(assetTypes, include)
		}
	}

	// Any narrowing at all, including one that names only feature flags: an include list
	// of just IAMPolicy would otherwise skip the asset pass entirely and leave no project.
	if len(gcp.Include) > 0 && !slices.Contains(assetTypes, ProjectAssetType) {
		assetTypes = append(assetTypes, ProjectAssetType)
	}

	return assetTypes
}
