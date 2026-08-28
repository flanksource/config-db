package processors

import (
	"encoding/json"
	"os"
	"testing"

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

func TestCNPGPluginGeneratesTypedBackupChangesAndPreservesConfig(t *testing.T) {
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
		name               string
		phase              string
		startedAt          string
		stoppedAt          string
		expectedChangeType string
		expectedStatus     types.Status
		expectedSeverity   string
	}{
		{
			name:               "completed",
			phase:              "completed",
			startedAt:          "2026-06-08T03:31:27Z",
			stoppedAt:          "2026-06-08T03:35:27Z",
			expectedChangeType: types.ChangeTypeBackupCompleted,
			expectedStatus:     types.StatusCompleted,
			expectedSeverity:   "info",
		},
		{
			name:               "failed",
			phase:              "failed",
			expectedChangeType: types.ChangeTypeBackupFailed,
			expectedStatus:     types.StatusFailed,
			expectedSeverity:   "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := map[string]any{"phase": tt.phase}
			if tt.startedAt != "" {
				status["startedAt"] = tt.startedAt
			}
			if tt.stoppedAt != "" {
				status["stoppedAt"] = tt.stoppedAt
			}

			config := map[string]any{
				"apiVersion": "postgresql.cnpg.io/v1",
				"kind":       "Backup",
				"metadata": map[string]any{
					"uid":               "backup-uid",
					"name":              "orders-20260608033127",
					"namespace":         "database",
					"creationTimestamp": "2026-06-08T03:31:00Z",
				},
				"spec": map[string]any{
					"method":  "plugin",
					"cluster": map[string]any{"name": "Orders"},
				},
				"status": status,
			}
			result := v1.ScrapeResult{
				ID:     "backup-uid",
				Name:   "orders-20260608033127",
				Type:   "Kubernetes::Backup",
				Config: config,
				Tags: v1.JSONStringMap{
					"cluster":   "Production",
					"namespace": "Database",
				},
			}

			if err := applyConfigMappings(ctx, &result, plugin.Spec.Configs.Mapping); err != nil {
				t.Fatalf("failed to apply config mapping: %v", err)
			}
			if err := applyConfigChangeGenerators(ctx, &result, plugin.Spec.Change.Generate); err != nil {
				t.Fatalf("failed to generate backup change: %v", err)
			}

			if result.Type != "CNPG::Backup" {
				t.Fatalf("expected mapped config type CNPG::Backup, got %q", result.Type)
			}
			if result.Config == nil || result.ID != "backup-uid" {
				t.Fatal("generator did not preserve the source backup config item")
			}
			if len(result.Changes) != 1 {
				t.Fatalf("expected one generated change, got %d", len(result.Changes))
			}

			change := result.Changes[0]
			if change.ChangeType != tt.expectedChangeType {
				t.Errorf("expected change type %q, got %q", tt.expectedChangeType, change.ChangeType)
			}
			if change.ExternalID != "kubernetes/production/cluster/database/orders" {
				t.Errorf("unexpected target external ID %q", change.ExternalID)
			}
			if change.ConfigType != "CNPG::Cluster" {
				t.Errorf("expected target type CNPG::Cluster, got %q", change.ConfigType)
			}
			if change.ExternalChangeID != "backup-uid" {
				t.Errorf("expected stable external change ID backup-uid, got %q", change.ExternalChangeID)
			}
			if change.Severity != tt.expectedSeverity {
				t.Errorf("expected severity %q, got %q", tt.expectedSeverity, change.Severity)
			}
			if _, ok := change.Details["raw"].(map[string]any); !ok {
				t.Errorf("expected raw CNPG backup object in change details, got %T", change.Details["raw"])
			}

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
			if backup.Status != tt.expectedStatus || backup.BackupType != types.BackupTypeStorageBackup {
				t.Errorf("unexpected typed backup details: status=%q backup_type=%q", backup.Status, backup.BackupType)
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
