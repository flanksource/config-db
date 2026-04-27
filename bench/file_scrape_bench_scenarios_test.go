//go:build bench
package bench

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	configdbmodels "github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	dutytypes "github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gopkg.in/yaml.v3"
)

const (
	benchScenarioDir       = "testdata/scrape_bench"
	defaultSampleInterval  = 25 * time.Millisecond
	defaultDBMetricMode    = "auto"
	defaultOverlapRatio    = 0.0
	benchConfigType        = "Benchmark::ConfigItem"
	benchConfigClass       = "Node"
	benchRoleType          = "BenchmarkRole"
	benchUserType          = "BenchmarkUser"
	benchChangeType        = "diff"
	benchAnalysisType      = dutymodels.AnalysisTypePerformance
	benchAnalysisSeverity  = dutymodels.SeverityLow
	benchAnalysisStatus    = dutymodels.AnalysisStatusOpen
	benchAccessSource      = "benchmark"
	benchConfigSource      = "benchmark-file"
	benchConfigSpecVersion = "bench.flanksource.com/v1"
)

type benchmarkEntityCounts struct {
	Configs      int `yaml:"configs"`
	Changes      int `yaml:"changes"`
	Analysis     int `yaml:"analysis"`
	ConfigAccess int `yaml:"config_access"`
	AccessLogs   int `yaml:"access_logs"`
}

type benchmarkDBMetricConfig struct {
	PGStatStatements string `yaml:"pg_stat_statements"`
	QueryLog         string `yaml:"query_log"`
}

type benchmarkScenario struct {
	Name           string                  `yaml:"name"`
	SampleInterval durationValue           `yaml:"sample_interval"`
	OverlapRatio   float64                 `yaml:"overlap_ratio"`
	Preseed        benchmarkEntityCounts   `yaml:"preseed"`
	Scrape         benchmarkEntityCounts   `yaml:"scrape"`
	DBMetrics      benchmarkDBMetricConfig `yaml:"db_metrics"`
}

type durationValue struct {
	time.Duration
}

func (d *durationValue) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar")
	}
	if strings.TrimSpace(value.Value) == "" {
		d.Duration = 0
		return nil
	}
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	d.Duration = parsed
	return nil
}

type benchmarkDataset struct {
	Items         []map[string]any
	Configs       []dutymodels.ConfigItem
	Changes       []configdbmodels.ConfigChange
	Analysis      []dutymodels.ConfigAnalysis
	Users         []dutymodels.ExternalUser
	Roles         []dutymodels.ExternalRole
	ConfigAccess  []dutymodels.ConfigAccess
	AccessLogs    []dutymodels.ConfigAccessLog
	OverlapConfig int
}

type benchmarkConfigRef struct {
	ID         uuid.UUID
	ExternalID string
	Name       string
}

func loadBenchmarkScenarios(tb testing.TB) []benchmarkScenario {
	tb.Helper()

	entries, err := os.ReadDir(benchScenarioDir)
	if err != nil {
		tb.Fatalf("failed to read benchmark scenario dir: %v", err)
	}

	filter := parseScenarioFilter(os.Getenv("CONFIG_DB_BENCH_SCENARIOS"))
	scenarios := make([]benchmarkScenario, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		path := filepath.Join(benchScenarioDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("failed to read scenario %s: %v", path, err)
		}

		var scenario benchmarkScenario
		if err := yaml.Unmarshal(raw, &scenario); err != nil {
			tb.Fatalf("failed to parse scenario %s: %v", path, err)
		}
		if scenario.Name == "" {
			tb.Fatalf("scenario %s is missing name", path)
		}
		if len(filter) > 0 && !filter[scenario.Name] {
			continue
		}
		normalizeBenchmarkScenario(tb, path, &scenario)
		scenarios = append(scenarios, scenario)
	}

	slices.SortFunc(scenarios, func(a, b benchmarkScenario) int {
		return strings.Compare(a.Name, b.Name)
	})

	if len(filter) > 0 && len(scenarios) == 0 {
		tb.Fatalf("CONFIG_DB_BENCH_SCENARIOS=%q did not match any scenarios", os.Getenv("CONFIG_DB_BENCH_SCENARIOS"))
	}
	if len(scenarios) == 0 {
		tb.Fatalf("no benchmark scenarios found in %s", benchScenarioDir)
	}
	return scenarios
}

func parseScenarioFilter(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make(map[string]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out[part] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeBenchmarkScenario(tb testing.TB, path string, scenario *benchmarkScenario) {
	tb.Helper()

	if scenario.SampleInterval.Duration <= 0 {
		scenario.SampleInterval.Duration = defaultSampleInterval
	}
	if scenario.OverlapRatio == 0 {
		scenario.OverlapRatio = defaultOverlapRatio
	}
	if scenario.OverlapRatio < 0 || scenario.OverlapRatio > 1 {
		tb.Fatalf("scenario %s has invalid overlap_ratio %.3f", path, scenario.OverlapRatio)
	}

	if scenario.DBMetrics.PGStatStatements == "" {
		scenario.DBMetrics.PGStatStatements = defaultDBMetricMode
	}
	if scenario.DBMetrics.QueryLog == "" {
		scenario.DBMetrics.QueryLog = defaultDBMetricMode
	}

	validateScenarioCounts(tb, path, "preseed", scenario.Preseed)
	validateScenarioCounts(tb, path, "scrape", scenario.Scrape)
}

func validateScenarioCounts(tb testing.TB, path, label string, counts benchmarkEntityCounts) {
	tb.Helper()

	if counts.Configs < 0 || counts.Changes < 0 || counts.Analysis < 0 || counts.ConfigAccess < 0 || counts.AccessLogs < 0 {
		tb.Fatalf("scenario %s has negative %s counts", path, label)
	}
	dependents := counts.Changes + counts.Analysis + counts.ConfigAccess + counts.AccessLogs
	if counts.Configs == 0 && dependents > 0 {
		tb.Fatalf("scenario %s has %s dependents without configs", path, label)
	}
}

func buildPreseedDataset(tb testing.TB, scenario benchmarkScenario, scraperID uuid.UUID) benchmarkDataset {
	tb.Helper()
	return buildBenchmarkDataset(tb, scenario.Name, "preseed", scenario.Preseed, nil, 0, scraperID, time.Now().Add(-48*time.Hour))
}

func buildScrapeDataset(tb testing.TB, scenario benchmarkScenario, overlapWith []benchmarkConfigRef) benchmarkDataset {
	tb.Helper()

	runScraperID := mustDeterministicUUID("bench-run-scraper:" + scenario.Name)
	overlapConfigs := 0
	if scenario.Scrape.Configs > 0 && len(overlapWith) > 0 && scenario.OverlapRatio > 0 {
		overlapConfigs = int(math.Round(float64(scenario.Scrape.Configs) * scenario.OverlapRatio))
		if overlapConfigs > len(overlapWith) {
			overlapConfigs = len(overlapWith)
		}
		if overlapConfigs > scenario.Scrape.Configs {
			overlapConfigs = scenario.Scrape.Configs
		}
	}

	dataset := buildBenchmarkDataset(tb, scenario.Name, "scrape", scenario.Scrape, overlapWith, overlapConfigs, runScraperID, time.Now().Add(-5*time.Minute))
	dataset.OverlapConfig = overlapConfigs
	return dataset
}

func buildBenchmarkDataset(tb testing.TB, scenarioName, phase string, counts benchmarkEntityCounts, overlapWith []benchmarkConfigRef, overlapConfigs int, scraperID uuid.UUID, baseTime time.Time) benchmarkDataset {
	tb.Helper()

	dataset := benchmarkDataset{
		Items:        make([]map[string]any, 0, counts.Configs),
		Configs:      make([]dutymodels.ConfigItem, 0, counts.Configs),
		Changes:      make([]configdbmodels.ConfigChange, 0, counts.Changes),
		Analysis:     make([]dutymodels.ConfigAnalysis, 0, counts.Analysis),
		Users:        make([]dutymodels.ExternalUser, 0, max(counts.ConfigAccess, counts.AccessLogs)),
		Roles:        make([]dutymodels.ExternalRole, 0, counts.ConfigAccess),
		ConfigAccess: make([]dutymodels.ConfigAccess, 0, counts.ConfigAccess),
		AccessLogs:   make([]dutymodels.ConfigAccessLog, 0, counts.AccessLogs),
	}

	configRefs := make([]benchmarkConfigRef, counts.Configs)
	for i := range counts.Configs {
		var ref benchmarkConfigRef
		if i < overlapConfigs && i < len(overlapWith) {
			ref = overlapWith[i]
		} else {
			ref = benchmarkConfigRef{
				ID:         mustDeterministicUUID(fmt.Sprintf("bench-config:%s:%s:%d", scenarioName, phase, i)),
				ExternalID: fmt.Sprintf("bench-%s-%s-config-%06d", scenarioName, phase, i),
				Name:       fmt.Sprintf("%s-%s-config-%06d", scenarioName, phase, i),
			}
		}
		configRefs[i] = ref

		createdAt := baseTime.Add(time.Duration(i) * time.Second)
		updatedAt := createdAt.Add(15 * time.Minute)
		configJSON := marshalBenchmarkConfig(tb, map[string]any{
			"apiVersion": benchConfigSpecVersion,
			"kind":       "ConfigItem",
			"metadata": map[string]any{
				"name": ref.Name,
				"labels": map[string]any{
					"scenario": scenarioName,
					"phase":    phase,
				},
			},
			"spec": map[string]any{
				"revision": i + 1,
				"enabled":  true,
				"port":     8080 + (i % 10),
			},
		})

		item := map[string]any{
			"id":          ref.ExternalID,
			"external_id": ref.ExternalID,
			"name":        ref.Name,
			"type":        benchConfigType,
			"class":       benchConfigClass,
			"config": map[string]any{
				"apiVersion": benchConfigSpecVersion,
				"kind":       "ConfigItem",
				"metadata": map[string]any{
					"name": ref.Name,
					"labels": map[string]any{
						"scenario": scenarioName,
						"phase":    phase,
					},
				},
				"spec": map[string]any{
					"revision": i + 1,
					"enabled":  true,
					"port":     8080 + (i % 10),
				},
			},
		}
		dataset.Items = append(dataset.Items, item)
		dataset.Configs = append(dataset.Configs, dutymodels.ConfigItem{
			ID:          ref.ID,
			ScraperID:   stringPtr(scraperID.String()),
			ConfigClass: benchConfigClass,
			ExternalID:  pq.StringArray{v1.NormalizeExternalID(ref.ExternalID)},
			Type:        stringPtr(benchConfigType),
			Name:        stringPtr(ref.Name),
			Source:      stringPtr(benchConfigSource),
			Config:      stringPtr(configJSON),
			Tags: dutytypes.JSONStringMap{
				"suite":    "file-scrape-benchmark",
				"scenario": scenarioName,
				"phase":    phase,
			},
			CreatedAt: createdAt,
			UpdatedAt: &updatedAt,
		})
	}

	for i := range max(counts.ConfigAccess, counts.AccessLogs) {
		userID := mustDeterministicUUID(fmt.Sprintf("bench-user:%s:%s:%d", scenarioName, phase, i))
		createdAt := baseTime.Add(time.Duration(i) * time.Second)
		updatedAt := createdAt.Add(30 * time.Minute)
		dataset.Users = append(dataset.Users, dutymodels.ExternalUser{
			ID:        userID,
			Aliases:   pq.StringArray{fmt.Sprintf("bench-user-%s-%s-%06d", scenarioName, phase, i)},
			Name:      fmt.Sprintf("Bench User %s %s %06d", scenarioName, phase, i),
			Tenant:    "bench",
			UserType:  benchUserType,
			ScraperID: scraperID,
			CreatedAt: createdAt,
			UpdatedAt: &updatedAt,
		})
	}

	for i := range counts.ConfigAccess {
		roleID := mustDeterministicUUID(fmt.Sprintf("bench-role:%s:%s:%d", scenarioName, phase, i))
		createdAt := baseTime.Add(time.Duration(i) * time.Second)
		updatedAt := createdAt.Add(45 * time.Minute)
		dataset.Roles = append(dataset.Roles, dutymodels.ExternalRole{
			ID:          roleID,
			Tenant:      "bench",
			ScraperID:   uuidPtr(scraperID),
			Aliases:     pq.StringArray{fmt.Sprintf("bench-role-%s-%s-%06d", scenarioName, phase, i)},
			RoleType:    benchRoleType,
			Name:        fmt.Sprintf("Bench Role %s %s %06d", scenarioName, phase, i),
			Description: "benchmark role",
			CreatedAt:   createdAt,
			UpdatedAt:   &updatedAt,
		})
	}

	for i := range counts.Changes {
		configRef := configRefs[i%len(configRefs)]
		createdAt := baseTime.Add(time.Duration(i) * time.Second)
		changeID := fmt.Sprintf("bench-change:%s:%s:%d", scenarioName, phase, i)
		patches := fmt.Sprintf(`[{"op":"replace","path":"/spec/revision","value":%d}]`, i+1)
		summary := fmt.Sprintf("benchmark change %d for %s", i, configRef.ExternalID)

		dataset.Changes = append(dataset.Changes, configdbmodels.ConfigChange{
			ID:               mustDeterministicUUID("bench-change-row:" + changeID).String(),
			ConfigID:         configRef.ID.String(),
			ChangeType:       benchChangeType,
			Severity:         string(benchAnalysisSeverity),
			Source:           benchAccessSource,
			Summary:          summary,
			Patches:          patches,
			Details:          v1.JSON{"index": i, "phase": phase},
			Count:            1,
			CreatedAt:        createdAt,
			ExternalChangeID: stringPtr(changeID),
		})

		if phase == "scrape" {
			item := dataset.Items[i%len(dataset.Items)]
			changeEntries, _ := item["changes"].([]map[string]any)
			changeEntries = append(changeEntries, map[string]any{
				"external_change_id": changeID,
				"change_type":        benchChangeType,
				"severity":           string(benchAnalysisSeverity),
				"source":             benchAccessSource,
				"summary":            summary,
				"patches":            patches,
				"created_at":         createdAt.Format(time.RFC3339),
				"details": map[string]any{
					"index": i,
					"phase": phase,
				},
			})
			item["changes"] = changeEntries
		}
	}

	for i := range counts.Analysis {
		configRef := configRefs[i%len(configRefs)]
		createdAt := baseTime.Add(time.Duration(i) * time.Second)
		externalAnalysisID := fmt.Sprintf("bench-analysis:%s:%s:%d", scenarioName, phase, i)
		dataset.Analysis = append(dataset.Analysis, dutymodels.ConfigAnalysis{
			ID:            db.GenerateAnalysisID(externalAnalysisID),
			ConfigID:      configRef.ID,
			ScraperID:     uuidPtr(scraperID),
			Analyzer:      fmt.Sprintf("BenchAnalyzer-%02d", i%4),
			Message:       fmt.Sprintf("benchmark analysis %d", i),
			Summary:       fmt.Sprintf("benchmark insight %d for %s", i, configRef.ExternalID),
			Status:        benchAnalysisStatus,
			Severity:      benchAnalysisSeverity,
			AnalysisType:  benchAnalysisType,
			Analysis:      dutytypes.JSONMap{"index": i, "phase": phase},
			Source:        benchAccessSource,
			FirstObserved: timePtr(createdAt),
			LastObserved:  timePtr(createdAt),
		})

		if phase == "scrape" {
			item := dataset.Items[i%len(dataset.Items)]
			analysisEntries, _ := item["analysis"].([]map[string]any)
			analysisEntries = append(analysisEntries, map[string]any{
				"external_analysis_id": externalAnalysisID,
				"summary":              fmt.Sprintf("benchmark insight %d for %s", i, configRef.ExternalID),
				"analysis": map[string]any{
					"index": i,
					"phase": phase,
				},
				"analysis_type":  string(benchAnalysisType),
				"severity":       string(benchAnalysisSeverity),
				"source":         benchAccessSource,
				"analyzer":       fmt.Sprintf("BenchAnalyzer-%02d", i%4),
				"messages":       []string{fmt.Sprintf("benchmark analysis %d", i)},
				"status":         benchAnalysisStatus,
				"first_observed": createdAt.Format(time.RFC3339),
				"last_observed":  createdAt.Format(time.RFC3339),
			})
			item["analysis"] = analysisEntries
		}
	}

	for i := range counts.ConfigAccess {
		configRef := configRefs[i%len(configRefs)]
		user := dataset.Users[i%len(dataset.Users)]
		role := dataset.Roles[i]
		createdAt := baseTime.Add(time.Duration(i) * time.Second)
		row := dutymodels.ConfigAccess{
			ID:             mustDeterministicUUID(fmt.Sprintf("bench-config-access:%s:%s:%d", scenarioName, phase, i)).String(),
			ScraperID:      uuidPtr(scraperID),
			Source:         stringPtr(benchAccessSource),
			ConfigID:       configRef.ID,
			ExternalUserID: uuidPtr(user.ID),
			ExternalRoleID: uuidPtr(role.ID),
			CreatedAt:      createdAt,
		}
		dataset.ConfigAccess = append(dataset.ConfigAccess, row)

		if phase == "scrape" {
			item := dataset.Items[i%len(dataset.Items)]
			entries, _ := item["config_access"].([]map[string]any)
			entries = append(entries, map[string]any{
				"created_at":       createdAt.Format(time.RFC3339),
				"external_user_id": user.ID.String(),
				"external_role_id": role.ID.String(),
				"source":           benchAccessSource,
			})
			item["config_access"] = entries
		}
	}

	for i := range counts.AccessLogs {
		configRef := configRefs[i%len(configRefs)]
		user := dataset.Users[i%len(dataset.Users)]
		createdAt := baseTime.Add(time.Duration(i) * time.Second)
		count := 1
		row := dutymodels.ConfigAccessLog{
			ConfigID:       configRef.ID,
			ExternalUserID: user.ID,
			ScraperID:      scraperID,
			CreatedAt:      createdAt,
			MFA:            i%2 == 0,
			Properties:     dutytypes.JSONMap{"index": i, "phase": phase},
			Count:          &count,
		}
		dataset.AccessLogs = append(dataset.AccessLogs, row)

		if phase == "scrape" {
			item := dataset.Items[i%len(dataset.Items)]
			entries, _ := item["access_logs"].([]map[string]any)
			entries = append(entries, map[string]any{
				"created_at":       createdAt.Format(time.RFC3339),
				"external_user_id": user.ID.String(),
				"mfa":              i%2 == 0,
				"properties": map[string]any{
					"index": i,
					"phase": phase,
				},
			})
			item["access_logs"] = entries
		}
	}

	return dataset
}

func writeScenarioScrapeFile(tb testing.TB, scenario benchmarkScenario, dataset benchmarkDataset) string {
	tb.Helper()

	dir := tb.TempDir()
	path := filepath.Join(dir, scenario.Name+".json")
	payload := map[string]any{
		"id":    scenario.Name + "-envelope",
		"name":  scenario.Name + "-envelope",
		"type":  benchConfigType,
		"class": benchConfigClass,
		"items": dataset.Items,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		tb.Fatalf("failed to marshal scrape payload: %v", err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		tb.Fatalf("failed to write scrape payload: %v", err)
	}
	return path
}

func benchmarkConfigRefs(items []dutymodels.ConfigItem) []benchmarkConfigRef {
	out := make([]benchmarkConfigRef, 0, len(items))
	for _, item := range items {
		extID := ""
		if len(item.ExternalID) > 0 {
			extID = item.ExternalID[0]
		}
		out = append(out, benchmarkConfigRef{
			ID:         item.ID,
			ExternalID: extID,
			Name:       stringValue(item.Name),
		})
	}
	return out
}

func marshalBenchmarkConfig(tb testing.TB, v any) string {
	tb.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		tb.Fatalf("failed to marshal benchmark config: %v", err)
	}
	return string(raw)
}

func mustDeterministicUUID(input string) uuid.UUID {
	sum := sha1.Sum([]byte(input))
	return uuid.NewSHA1(uuid.NameSpaceOID, sum[:])
}

func stringPtr(v string) *string {
	return &v
}

func uuidPtr(v uuid.UUID) *uuid.UUID {
	return &v
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
