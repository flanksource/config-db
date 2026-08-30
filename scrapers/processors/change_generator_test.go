package processors

import (
	"strings"
	"testing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutycontext "github.com/flanksource/duty/context"
)

func TestConfigChangeGeneratorsIsolateFailuresAndInheritConfigIdentity(t *testing.T) {
	ctx := api.NewScrapeContext(dutycontext.New())
	result := v1.ScrapeResult{
		ID:     "config-uid",
		Type:   "Example::Config",
		Config: map[string]any{"name": "example"},
	}
	generators := []v1.ConfigChangeGenerator{
		{
			Filter: `missingFilterFunction()`,
			Expr:   `[].toJSON()`,
		},
		{
			Expr: `missingFunction()`,
		},
		{
			Expr: `"not-json"`,
		},
		{
			Expr: `[{
				"external_change_id": "change-1",
				"change_type": "Generated",
				"summary": "generated after failures"
			}].toJSON()`,
		},
	}

	applyConfigChangeGenerators(ctx, &result, generators)

	if len(result.Changes) != 1 {
		t.Fatalf("expected the valid generator to run after failures, got %d changes", len(result.Changes))
	}
	if result.Changes[0].ExternalID != result.ID || result.Changes[0].ConfigType != result.Type {
		t.Fatalf("expected inherited config identity, got external_id=%q config_type=%q", result.Changes[0].ExternalID, result.Changes[0].ConfigType)
	}
	if len(result.Warnings) != 3 {
		t.Fatalf("expected three isolated generator warnings, got %d: %#v", len(result.Warnings), result.Warnings)
	}
	for _, warning := range result.Warnings {
		if !strings.Contains(warning.Error, "config change generator") {
			t.Errorf("unexpected warning: %q", warning.Error)
		}
	}
}
