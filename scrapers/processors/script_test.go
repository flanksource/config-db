package processors

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
)

// TestUnmarshalConfigsFromString_LiftsTopLevelFields guards the fix for the
// entra.yaml Azure::Group regression where CEL-produced results rendered in
// the UI with no name or id. Root cause: the old implementation decoded each
// CEL output entry only into a bare map[string]interface{} and stuffed the
// whole thing into `.Config`, so keys like `name`/`config_type`/`id`/`aliases`
// never reached the struct fields that downstream rendering reads.
func TestUnmarshalConfigsFromString_LiftsTopLevelFields(t *testing.T) {
	in := `[
	  {
	    "id": "group-1",
	    "name": "Group One",
	    "config_type": "Azure::Group",
	    "config_class": "security",
	    "aliases": ["alias-a", "alias-b"],
	    "config": {"displayName": "Group One", "createdDateTime": "2024-01-01"},
	    "external_groups": [{"name": "Group One", "id": "group-1"}]
	  }
	]`

	got, err := unmarshalConfigsFromString(in, v1.ScrapeResult{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}

	r := got[0]
	if r.ID != "group-1" {
		t.Errorf("ID not lifted: want %q, got %q", "group-1", r.ID)
	}
	if r.Name != "Group One" {
		t.Errorf("Name not lifted: want %q, got %q", "Group One", r.Name)
	}
	if r.Type != "Azure::Group" {
		t.Errorf("Type not lifted: want %q, got %q", "Azure::Group", r.Type)
	}
	if r.ConfigClass != "security" {
		t.Errorf("ConfigClass not lifted: want %q, got %q", "security", r.ConfigClass)
	}
	if len(r.Aliases) != 2 || r.Aliases[0] != "alias-a" || r.Aliases[1] != "alias-b" {
		t.Errorf("Aliases not lifted: got %#v", r.Aliases)
	}

	// .Config must retain the FULL outer map so ExtractFullMode can still
	// pull external_groups / external_user_groups from it. Do NOT collapse to
	// just the nested `config` sub-dict — that replacement happens later in
	// the pipeline.
	cfg, ok := r.Config.(map[string]interface{})
	if !ok {
		t.Fatalf(".Config should be map[string]interface{}, got %T", r.Config)
	}
	if _, ok := cfg["external_groups"]; !ok {
		t.Errorf(".Config missing external_groups key — outer map not preserved: %#v", cfg)
	}
	if nested, ok := cfg["config"].(map[string]interface{}); !ok {
		t.Errorf(".Config['config'] should be a nested map, got %T", cfg["config"])
	} else if nested["displayName"] != "Group One" {
		t.Errorf(".Config['config']['displayName'] = %v, want Group One", nested["displayName"])
	}
}

// TestUnmarshalConfigsFromString_MetadataOnly verifies that a CEL entry with
// only an `external_users` / `users` payload and no id/name still round-trips
// cleanly — the struct fields stay empty and the full map is retained in
// .Config so the downstream extraction pipeline can find the entities.
func TestUnmarshalConfigsFromString_MetadataOnly(t *testing.T) {
	in := `[
	  {
	    "users": [{"id": "u1", "name": "Alice"}, {"id": "u2", "name": "Bob"}]
	  }
	]`

	got, err := unmarshalConfigsFromString(in, v1.ScrapeResult{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}

	r := got[0]
	if r.ID != "" || r.Name != "" || r.Type != "" {
		t.Errorf("expected empty top-level fields, got ID=%q Name=%q Type=%q", r.ID, r.Name, r.Type)
	}

	cfg, ok := r.Config.(map[string]interface{})
	if !ok {
		t.Fatalf(".Config should be map, got %T", r.Config)
	}
	if _, ok := cfg["users"]; !ok {
		t.Errorf(".Config missing users key: %#v", cfg)
	}
}

// TestUnmarshalConfigsFromString_UUIDAsID verifies that a CEL entry which
// uses `uuid` as its canonical-id key lands on ScrapeResult.ID. This is the
// project's convention: `uuid` is the explicit marker for "this string is a
// UUID that uniquely identifies the external entity".
func TestUnmarshalConfigsFromString_UUIDAsID(t *testing.T) {
	in := `[
	  {
	    "uuid": "2bf9b41f-93ae-0cfc-ff61-3935e010b865",
	    "name": "Group With UUID",
	    "config_type": "Azure::Group",
	    "config": {"displayName": "Group With UUID"}
	  }
	]`

	got, err := unmarshalConfigsFromString(in, v1.ScrapeResult{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	r := got[0]
	if r.ID != "2bf9b41f-93ae-0cfc-ff61-3935e010b865" {
		t.Errorf("uuid not lifted to ID: got %q", r.ID)
	}
	if r.Name != "Group With UUID" {
		t.Errorf("Name not lifted: got %q", r.Name)
	}
}

// TestUnmarshalConfigsFromString_ExplicitIDBeatsUUID verifies that when both
// `id` and `uuid` are present, the explicit `id` wins — i.e. uuid is only a
// fallback when no explicit id is provided.
func TestUnmarshalConfigsFromString_ExplicitIDBeatsUUID(t *testing.T) {
	in := `[
	  {
	    "id": "explicit-id",
	    "uuid": "2bf9b41f-93ae-0cfc-ff61-3935e010b865",
	    "name": "Group With Both"
	  }
	]`

	got, err := unmarshalConfigsFromString(in, v1.ScrapeResult{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].ID != "explicit-id" {
		t.Errorf("explicit id should win: got %q", got[0].ID)
	}
}

// TestUnmarshalConfigsFromString_MixedBatch covers the full entra.yaml CEL
// shape: one metadata-only synthetic entry followed by N per-group entries.
// Each type is handled independently — the struct fields get populated for
// the group entries but not the synthetic one.
func TestUnmarshalConfigsFromString_MixedBatch(t *testing.T) {
	in := `[
	  {"users": [{"id": "u1"}]},
	  {
	    "id": "group-a",
	    "name": "Group A",
	    "config_type": "Azure::Group",
	    "config": {"displayName": "Group A"}
	  },
	  {
	    "id": "group-b",
	    "name": "Group B",
	    "config_type": "Azure::Group",
	    "config": {"displayName": "Group B"}
	  }
	]`

	got, err := unmarshalConfigsFromString(in, v1.ScrapeResult{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}

	if got[0].ID != "" || got[0].Name != "" {
		t.Errorf("synthetic entry should have empty ID/Name, got ID=%q Name=%q", got[0].ID, got[0].Name)
	}
	if got[1].ID != "group-a" || got[1].Name != "Group A" {
		t.Errorf("group-a: ID=%q Name=%q", got[1].ID, got[1].Name)
	}
	if got[2].ID != "group-b" || got[2].Name != "Group B" {
		t.Errorf("group-b: ID=%q Name=%q", got[2].ID, got[2].Name)
	}
}

func TestScriptEnv_ExcludesLastScrapeSummary(t *testing.T) {
	env := scriptEnv(v1.ScrapeResult{Config: map[string]any{"name": "demo"}})

	if _, ok := env["last_scrape_summary"]; ok {
		t.Fatalf("script env should not include last_scrape_summary")
	}
	if _, ok := env["config"]; !ok {
		t.Fatalf("script env missing config")
	}
	if _, ok := env["result"]; !ok {
		t.Fatalf("script env missing result")
	}
}
