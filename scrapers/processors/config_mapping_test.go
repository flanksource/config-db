package processors

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutycontext "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/types"
	"sigs.k8s.io/yaml"
)

func TestApplyConfigMappings(t *testing.T) {
	ctx := api.NewScrapeContext(dutycontext.New())
	newResult := func() v1.ScrapeResult {
		return v1.ScrapeResult{
			Type: "Kubernetes::CNPGCluster",
			Config: map[string]any{
				"apiVersion": "postgresql.cnpg.io/v1",
				"kind":       "Cluster",
			},
		}
	}

	t.Run("evaluates against the full config and maps an expression output", func(t *testing.T) {
		result := newResult()
		mappings := []v1.ConfigMapping{{
			Match: `config.apiVersion.startsWith("postgresql.cnpg.io/")`,
			Type:  types.ValueExpression{Expr: `"CNPG::" + config.kind`},
		}}

		if err := applyConfigMappings(ctx, &result, mappings); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Type != "CNPG::Cluster" {
			t.Fatalf("expected mapped type, got %q", result.Type)
		}
		if got := result.AsMap()["config_type"]; got != "CNPG::Cluster" {
			t.Fatalf("expected refreshed map, got config_type=%v", got)
		}
	})

	t.Run("first match wins without cascading", func(t *testing.T) {
		result := newResult()
		mappings := []v1.ConfigMapping{
			{
				Match: `config_type == "Kubernetes::CNPGCluster"`,
				Type:  types.ValueExpression{Value: "CNPG::Cluster"},
			},
			{
				Match: `config_type == "CNPG::Cluster"`,
				Type:  types.ValueExpression{Value: "Cascaded::Cluster"},
			},
		}

		if err := applyConfigMappings(ctx, &result, mappings); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Type != "CNPG::Cluster" {
			t.Fatalf("expected first mapping only, got %q", result.Type)
		}
	})

	t.Run("a non-match leaves the type unchanged", func(t *testing.T) {
		result := newResult()
		mappings := []v1.ConfigMapping{{
			Match: `config.apiVersion == "example.com/v1"`,
			Type:  types.ValueExpression{Value: "Example::Cluster"},
		}}

		if err := applyConfigMappings(ctx, &result, mappings); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Type != "Kubernetes::CNPGCluster" {
			t.Fatalf("expected original type, got %q", result.Type)
		}
	})

	t.Run("an empty output leaves the type unchanged", func(t *testing.T) {
		result := newResult()
		mappings := []v1.ConfigMapping{{Match: "true"}}

		if err := applyConfigMappings(ctx, &result, mappings); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Type != "Kubernetes::CNPGCluster" {
			t.Fatalf("expected original type, got %q", result.Type)
		}
	})

	t.Run("invalid CEL returns an error", func(t *testing.T) {
		result := newResult()
		mappings := []v1.ConfigMapping{{
			Match: `config_type ==`,
			Type:  types.ValueExpression{Value: "CNPG::Cluster"},
		}}

		if err := applyConfigMappings(ctx, &result, mappings); err == nil {
			t.Fatal("expected invalid CEL to return an error")
		}
	})
}

func TestConfigMappingSeesExtractedLabels(t *testing.T) {
	ctx := api.NewScrapeContext(dutycontext.New())
	input := v1.ScrapeResult{
		ID:          "resource-1",
		Name:        "resource-1",
		Type:        "Example::Old",
		ConfigClass: "Example",
		Config:      map[string]any{"name": "resource-1"},
		BaseScraper: v1.BaseScraper{
			Labels: v1.JSONStringMap{"environment": "production"},
			Transform: v1.Transform{
				Configs: v1.TransformConfigs{Mapping: []v1.ConfigMapping{{
					Match: `labels.environment == "production"`,
					Type:  types.ValueExpression{Value: "Example::Production"},
				}}},
			},
		},
	}

	result, err := (Extract{}).extractAttributes(ctx, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != "Example::Production" {
		t.Fatalf("expected mapping to see inherited labels, got %q", result.Type)
	}
}

// decodeBackupDetails asserts the change details round-trip into duty's typed
// Backup detail and returns it.
func decodeBackupDetails(t *testing.T, change v1.ChangeResult) types.Backup {
	t.Helper()

	detailJSON, err := json.Marshal(change.Details)
	if err != nil {
		t.Fatalf("failed to marshal generated details: %v", err)
	}
	detail, err := types.UnmarshalChangeDetails(detailJSON)
	if err != nil {
		t.Fatalf("generated details are not a typed backup: %v", err)
	}
	backup, ok := detail.(types.Backup)
	if !ok {
		t.Fatalf("expected types.Backup, got %T", detail)
	}
	return backup
}

func TestCNPGPluginGeneratesBackupLifecycleChanges(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/plugins/cnpg.yaml")
	if err != nil {
		t.Fatalf("failed to read CNPG plugin: %v", err)
	}

	var plugin v1.ScrapePlugin
	if err := yaml.Unmarshal(data, &plugin); err != nil {
		t.Fatalf("failed to parse CNPG plugin: %v", err)
	}

	ctx := api.NewScrapeContext(dutycontext.New()).WithScrapeConfig(&v1.ScrapeConfig{})
	tests := []struct {
		name             string
		phase            string
		method           string
		startedAt        string
		stoppedAt        string
		omitCreationTime bool
		errorMessage     string
		commandError     string

		expectStarted bool
		// An empty terminalChangeType means the backup has not terminated.
		terminalChangeType string
		terminalStatus     types.Status
		terminalSeverity   string
		expectedBackupType types.BackupType
	}{
		{
			name: "completed volume snapshot", phase: "completed", method: "volumeSnapshot",
			startedAt: "2026-06-08T03:31:27Z", stoppedAt: "2026-06-08T03:35:27Z",
			expectStarted: true, terminalChangeType: types.ChangeTypeBackupCompleted,
			terminalStatus: types.StatusCompleted, terminalSeverity: "info",
			expectedBackupType: types.BackupTypeSnapshot,
		},
		{
			name: "completed object store", phase: "completed", method: "barmanObjectStore",
			startedAt: "2026-06-08T03:31:27Z", stoppedAt: "2026-06-08T03:35:27Z",
			expectStarted: true, terminalChangeType: types.ChangeTypeBackupCompleted,
			terminalStatus: types.StatusCompleted, terminalSeverity: "info",
			expectedBackupType: types.BackupTypeStorageBackup,
		},
		{
			name: "failed after starting", phase: "failed", method: "plugin",
			startedAt: "2026-06-08T03:31:27Z", stoppedAt: "2026-06-08T03:35:27Z",
			errorMessage: "backup upload failed", commandError: "exit status 1",
			expectStarted: true, terminalChangeType: types.ChangeTypeBackupFailed,
			terminalStatus: types.StatusFailed, terminalSeverity: "high",
			expectedBackupType: types.BackupTypeStorageBackup,
		},
		{
			// Still in flight: a started row exists, the outcome does not yet.
			name: "running", phase: "running", method: "plugin",
			startedAt:     "2026-06-08T03:31:27Z",
			expectStarted: true,
		},
		{
			// CNPG never sets startedAt for this phase, so the backup never ran
			// and only its outcome is recorded.
			name: "wal archiving failing", phase: "walArchivingFailing", method: "plugin",
			commandError:  "WAL archiving is not working",
			expectStarted: false, terminalChangeType: types.ChangeTypeBackupFailed,
			terminalStatus: types.StatusFailed, terminalSeverity: "high",
			expectedBackupType: types.BackupTypeStorageBackup,
		},
		{
			name: "invalid backup definition", phase: "invalid backup definition", method: "plugin",
			errorMessage:  "backup configuration is invalid",
			expectStarted: false, terminalChangeType: types.ChangeTypeBackupFailed,
			terminalStatus: types.StatusFailed, terminalSeverity: "high",
			expectedBackupType: types.BackupTypeStorageBackup,
		},
		{
			name: "completed without any timestamps", phase: "completed", method: "plugin",
			omitCreationTime: true,
			expectStarted:    false, terminalChangeType: types.ChangeTypeBackupCompleted,
			terminalStatus: types.StatusCompleted, terminalSeverity: "info",
			expectedBackupType: types.BackupTypeStorageBackup,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := map[string]any{
				"phase":           tt.phase,
				"method":          tt.method,
				"backupId":        "20260608T033127",
				"destinationPath": "s3://backups/orders",
			}
			if tt.startedAt != "" {
				status["startedAt"] = tt.startedAt
			}
			if tt.stoppedAt != "" {
				status["stoppedAt"] = tt.stoppedAt
			}
			if tt.errorMessage != "" {
				status["error"] = tt.errorMessage
			}
			if tt.commandError != "" {
				status["commandError"] = tt.commandError
			}

			metadata := map[string]any{
				"uid":       "backup-uid",
				"name":      "orders-20260608033127",
				"namespace": "database",
			}
			if !tt.omitCreationTime {
				metadata["creationTimestamp"] = "2026-06-08T03:31:00Z"
			}

			result := v1.ScrapeResult{
				ID:   "backup-uid",
				Name: "orders-20260608033127",
				Type: "Kubernetes::Backup",
				Config: map[string]any{
					"apiVersion": "postgresql.cnpg.io/v1",
					"kind":       "Backup",
					"metadata":   metadata,
					"spec":       map[string]any{"cluster": map[string]any{"name": "Orders"}},
					"status":     status,
				},
				Tags: v1.JSONStringMap{
					"cluster":   "Production",
					"namespace": "Database",
				},
			}

			if err := applyConfigMappings(ctx, &result, plugin.Spec.Configs.Mapping); err != nil {
				t.Fatalf("failed to apply config mapping: %v", err)
			}
			applyConfigChangeGenerators(ctx, &result, plugin.Spec.Change.Generate)

			if len(result.Warnings) != 0 {
				t.Fatalf("unexpected generator warnings: %v", result.Warnings)
			}
			if result.Type != "CNPG::Backup" {
				t.Fatalf("expected mapped config type CNPG::Backup, got %q", result.Type)
			}
			if result.Config == nil || result.ID != "backup-uid" {
				t.Fatal("generator did not preserve the source backup config item")
			}

			expected := 0
			if tt.expectStarted {
				expected++
			}
			if tt.terminalChangeType != "" {
				expected++
			}
			if len(result.Changes) != expected {
				t.Fatalf("expected %d changes, got %d: %v", expected, len(result.Changes), result.Changes)
			}

			byChangeID := make(map[string]v1.ChangeResult, len(result.Changes))
			for _, change := range result.Changes {
				if _, duplicate := byChangeID[change.ExternalChangeID]; duplicate {
					t.Fatalf("two changes share external change id %q", change.ExternalChangeID)
				}
				byChangeID[change.ExternalChangeID] = change

				if change.ExternalID != "kubernetes/production/cluster/database/orders" {
					t.Errorf("unexpected target external ID %q", change.ExternalID)
				}
				if change.ConfigType != "CNPG::Cluster" {
					t.Errorf("expected target type CNPG::Cluster, got %q", change.ConfigType)
				}
				props, ok := change.Details["properties"].(map[string]any)
				if !ok {
					t.Fatalf("expected properties in change details, got %T", change.Details["properties"])
				}
				// backup_name keeps each backup's fingerprint distinct so the deduper
				// in db/update.go cannot merge two backups inside changes.dedup.window.
				if props["backup_name"] != "orders-20260608033127" {
					t.Errorf("expected backup_name to identify the backup, got %v", props["backup_name"])
				}
			}

			if tt.expectStarted {
				started, ok := byChangeID["backup-uid/started"]
				if !ok {
					t.Fatalf("missing the started change, got %v", byChangeID)
				}
				if started.ChangeType != types.ChangeTypeBackupStarted {
					t.Errorf("expected change type %q, got %q", types.ChangeTypeBackupStarted, started.ChangeType)
				}
				if started.Severity != "info" {
					t.Errorf("expected the started change to be informational, got %q", started.Severity)
				}
				if started.CreatedAt == nil || started.CreatedAt.UTC().Format(time.RFC3339) != tt.startedAt {
					t.Errorf("expected started at %q, got %v", tt.startedAt, started.CreatedAt)
				}
				backup := decodeBackupDetails(t, started)
				if backup.Status != types.StatusRunning {
					t.Errorf("expected a running backup detail, got %q", backup.Status)
				}
				if backup.EndTimestamp != "" {
					t.Errorf("expected no end timestamp on the started change, got %q", backup.EndTimestamp)
				}
			}

			if tt.terminalChangeType == "" {
				return
			}

			terminated, ok := byChangeID["backup-uid/terminated"]
			if !ok {
				t.Fatalf("missing the terminated change, got %v", byChangeID)
			}
			if terminated.ChangeType != tt.terminalChangeType {
				t.Errorf("expected change type %q, got %q", tt.terminalChangeType, terminated.ChangeType)
			}
			if terminated.Severity != tt.terminalSeverity {
				t.Errorf("expected severity %q, got %q", tt.terminalSeverity, terminated.Severity)
			}
			switch {
			case tt.stoppedAt != "":
				if terminated.CreatedAt == nil || terminated.CreatedAt.UTC().Format(time.RFC3339) != tt.stoppedAt {
					t.Errorf("expected the outcome to be dated %q, got %v", tt.stoppedAt, terminated.CreatedAt)
				}
			case tt.omitCreationTime:
				if terminated.CreatedAt != nil {
					t.Errorf("expected missing timestamps to leave created_at empty, got %v", terminated.CreatedAt)
				}
			}

			changeJSON, err := json.Marshal(terminated)
			if err != nil {
				t.Fatalf("failed to marshal generated change: %v", err)
			}
			var persistedChange v1.ChangeResult
			if err := json.Unmarshal(changeJSON, &persistedChange); err != nil {
				t.Fatalf("failed to round-trip generated change: %v", err)
			}
			if _, ok := persistedChange.Details["properties"].(map[string]any); !ok {
				t.Errorf("expected generic details JSON to preserve properties, got %T", persistedChange.Details["properties"])
			}

			backup := decodeBackupDetails(t, terminated)
			if backup.Status != tt.terminalStatus || backup.BackupType != tt.expectedBackupType {
				t.Errorf("unexpected typed backup details: status=%q backup_type=%q", backup.Status, backup.BackupType)
			}
			if backup.Event.Properties["method"] != tt.method || backup.Event.Properties["backup_id"] != "20260608T033127" || backup.Event.Properties["destination_path"] != "s3://backups/orders" {
				t.Errorf("missing promoted backup properties: %#v", backup.Event.Properties)
			}
			if backup.Event.Properties["error"] != tt.errorMessage || backup.Event.Properties["command_error"] != tt.commandError {
				t.Errorf("missing promoted failure details: %#v", backup.Event.Properties)
			}
		})
	}
}

func TestCNPGPluginSkipsBackupsThatHaveNotStarted(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/plugins/cnpg.yaml")
	if err != nil {
		t.Fatalf("failed to read CNPG plugin: %v", err)
	}

	var plugin v1.ScrapePlugin
	if err := yaml.Unmarshal(data, &plugin); err != nil {
		t.Fatalf("failed to parse CNPG plugin: %v", err)
	}

	ctx := api.NewScrapeContext(dutycontext.New()).WithScrapeConfig(&v1.ScrapeConfig{})

	// Neither started nor terminated: CNPG sets phase=running before it sets
	// startedAt, so a backup can be seen running with nothing to report yet.
	for _, phase := range []string{"pending", "started", "running"} {
		t.Run(phase, func(t *testing.T) {
			result := v1.ScrapeResult{
				ID:   "backup-uid",
				Name: "orders-20260608033127",
				Type: "Kubernetes::Backup",
				Config: map[string]any{
					"apiVersion": "postgresql.cnpg.io/v1",
					"kind":       "Backup",
					"metadata": map[string]any{
						"uid":               "backup-uid",
						"name":              "orders-20260608033127",
						"namespace":         "database",
						"creationTimestamp": "2026-06-08T03:31:00Z",
					},
					"spec":   map[string]any{"cluster": map[string]any{"name": "Orders"}},
					"status": map[string]any{"phase": phase, "method": "plugin"},
				},
				Tags: v1.JSONStringMap{
					"cluster":   "Production",
					"namespace": "Database",
				},
			}

			if err := applyConfigMappings(ctx, &result, plugin.Spec.Configs.Mapping); err != nil {
				t.Fatalf("failed to apply config mapping: %v", err)
			}
			applyConfigChangeGenerators(ctx, &result, plugin.Spec.Change.Generate)

			if len(result.Changes) != 0 {
				t.Fatalf("expected no change for phase %q, got %d: %v", phase, len(result.Changes), result.Changes)
			}
			if len(result.Warnings) != 0 {
				t.Fatalf("expected no warnings for phase %q, got %v", phase, result.Warnings)
			}
			if result.Config == nil || result.Type != "CNPG::Backup" {
				t.Fatal("skipping a phase must still preserve the backup config item")
			}
		})
	}
}

func TestCNPGPluginMappingAndRelationships(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/plugins/cnpg.yaml")
	if err != nil {
		t.Fatalf("failed to read CNPG plugin: %v", err)
	}

	var plugin v1.ScrapePlugin
	if err := yaml.Unmarshal(data, &plugin); err != nil {
		t.Fatalf("failed to parse CNPG plugin: %v", err)
	}

	ctx := api.NewScrapeContext(dutycontext.New()).WithScrapeConfig(&v1.ScrapeConfig{})
	nonKubernetes := v1.ScrapeResult{Type: "AWS::EC2::Instance", Config: "scalar config"}
	if err := applyConfigMappings(ctx, &nonKubernetes, plugin.Spec.Configs.Mapping); err != nil {
		t.Fatalf("CNPG mapping must ignore non-Kubernetes configs: %v", err)
	}
	if nonKubernetes.Type != "AWS::EC2::Instance" {
		t.Fatalf("CNPG mapping changed a non-Kubernetes type to %q", nonKubernetes.Type)
	}

	tests := []struct {
		name               string
		kind               string
		config             map[string]any
		expectedExternalID string
		expectedType       string
		expectedParent     bool
	}{
		{
			name: "backup to cluster",
			kind: "Backup",
			config: map[string]any{
				"spec": map[string]any{"cluster": map[string]any{"name": "Orders"}},
			},
			expectedExternalID: "kubernetes/production/cluster/database/orders",
			expectedType:       "CNPG::Cluster",
			expectedParent:     true,
		},
		{
			name: "database role to cluster",
			kind: "DatabaseRole",
			config: map[string]any{
				"spec": map[string]any{"cluster": map[string]any{"name": "Orders"}},
			},
			expectedExternalID: "kubernetes/production/cluster/database/orders",
			expectedType:       "CNPG::Cluster",
			expectedParent:     true,
		},
		{
			name:               "failover quorum to same-name cluster",
			kind:               "FailoverQuorum",
			config:             map[string]any{},
			expectedExternalID: "kubernetes/production/cluster/database/orders",
			expectedType:       "CNPG::Cluster",
			expectedParent:     true,
		},
		{
			name: "cluster to namespaced image catalog",
			kind: "Cluster",
			config: map[string]any{
				"spec": map[string]any{"imageCatalogRef": map[string]any{"kind": "ImageCatalog", "name": "Postgres"}},
			},
			expectedExternalID: "kubernetes/production/imagecatalog/database/postgres",
			expectedType:       "CNPG::ImageCatalog",
		},
		{
			name: "cluster to cluster image catalog",
			kind: "Cluster",
			config: map[string]any{
				"spec": map[string]any{"imageCatalogRef": map[string]any{"kind": "ClusterImageCatalog", "name": "Postgres"}},
			},
			expectedExternalID: "kubernetes/production/clusterimagecatalog//postgres",
			expectedType:       "CNPG::ClusterImageCatalog",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]any{
				"apiVersion": "postgresql.cnpg.io/v1",
				"kind":       tt.kind,
			}
			for k, v := range tt.config {
				config[k] = v
			}
			result := v1.ScrapeResult{
				Name:   "Orders",
				Type:   "Kubernetes::" + tt.kind,
				Config: config,
				Tags: v1.JSONStringMap{
					"cluster":   "Production",
					"namespace": "Database",
				},
			}

			if err := applyConfigMappings(ctx, &result, plugin.Spec.Configs.Mapping); err != nil {
				t.Fatalf("failed to apply config mapping: %v", err)
			}

			relationships, err := getRelationshipsFromRelationshipConfigs(ctx, result, plugin.Spec.Relationship)
			if err != nil {
				t.Fatalf("failed to evaluate relationships: %v", err)
			}
			if len(relationships) != 1 {
				t.Fatalf("expected one relationship, got %d", len(relationships))
			}
			if relationships[0].Selector.ExternalID != tt.expectedExternalID {
				t.Errorf("expected external ID %q, got %q", tt.expectedExternalID, relationships[0].Selector.ExternalID)
			}
			if relationships[0].Selector.Type != tt.expectedType {
				t.Errorf("expected type %q, got %q", tt.expectedType, relationships[0].Selector.Type)
			}
			if relationships[0].Parent != tt.expectedParent {
				t.Errorf("expected parent=%v, got %v", tt.expectedParent, relationships[0].Parent)
			}
		})
	}
}
