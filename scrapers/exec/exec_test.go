package exec

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestParseOutput_JSONObject(t *testing.T) {
	config := v1.Exec{}

	stdout := `{"id": "123", "name": "test", "type": "TestType"}`

	results := parseOutput(config, stdout)

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	if results[0].Error != nil {
		t.Errorf("Expected no error, got %v", results[0].Error)
	}
}

func TestParseOutput_JSONArray(t *testing.T) {
	config := v1.Exec{}

	stdout := `[{"id": "1", "name": "first"}, {"id": "2", "name": "second"}]`

	results := parseOutput(config, stdout)

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	for i, result := range results {
		if result.Error != nil {
			t.Errorf("Result %d has error: %v", i, result.Error)
		}
	}
}

func TestParseOutput_YAML(t *testing.T) {
	config := v1.Exec{}

	stdout := `
id: "123"
name: "test"
type: "TestType"
`

	results := parseOutput(config, stdout)

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	if results[0].Error != nil {
		t.Errorf("Expected no error, got %v", results[0].Error)
	}
}

func TestParseOutput_EmptyOutput(t *testing.T) {
	config := v1.Exec{}

	stdout := ``

	results := parseOutput(config, stdout)

	if len(results) != 0 {
		t.Errorf("Expected 0 results for empty output, got %d", len(results))
	}
}

func TestParseOutput_PlainText(t *testing.T) {
	config := v1.Exec{}

	stdout := `This is plain text output that is not JSON or YAML`

	results := parseOutput(config, stdout)

	if len(results) != 1 {
		t.Errorf("Expected 1 result for plain text, got %d", len(results))
	}

	if results[0].Error != nil {
		t.Errorf("Expected no error for plain text, got %v", results[0].Error)
	}

	// Plain text gets parsed as YAML, which treats it as a string
	// When converted to JSON, it becomes a quoted string
	expectedConfig := `"This is plain text output that is not JSON or YAML"`
	configStr, ok := results[0].Config.(string)
	if !ok {
		t.Errorf("Expected config to be a string, got %T", results[0].Config)
	} else if configStr != expectedConfig {
		t.Errorf("Expected config to be %q, got %q", expectedConfig, configStr)
	}
}
