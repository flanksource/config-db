package scrapers

import (
	"encoding/json"
	"os"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
	v1 "github.com/flanksource/config-db/api/v1"
)

func getConfig(t *testing.T, name string) v1.ConfigScraper {
	configs, err := v1.ParseConfigs("fixtures/" + name + ".yaml")
	if err != nil {
		t.Errorf("Failed to parse config: %v", err)
	}
	return configs[0]
}

func getFixtureResult(t *testing.T, fixture string) []v1.ScrapeResult {
	data, err := os.ReadFile("fixtures/expected/" + fixture + ".json")
	if err != nil {
		t.Errorf("Failed to read fixture: %v", err)
	}
	var results []v1.ScrapeResult

	if err := json.Unmarshal(data, &results); err != nil {
		t.Errorf("Failed to unmarshal fixture: %v", err)
	}
	return results
}

func TestRun(t *testing.T) {
	_ = os.Chdir("..")
	fixtures := []string{
		"file-git",
		"file-script",
		"file-mask",
	}

	for _, fixtureName := range fixtures {
		fixture := fixtureName
		t.Run(fixture, func(t *testing.T) {
			config := getConfig(t, fixture)
			expected := getFixtureResult(t, fixture)
			results, err := Run(&v1.ScrapeContext{}, config)

			if err != nil {
				t.Errorf("Unexpected error:%s", err.Error())
			}

			if len(results) != len(expected) {
				t.Errorf("expected %d results, got: %d", len(expected), len(results))
				return
			}

			for i := 0; i < len(expected); i++ {
				want := expected[i]
				got := results[i]

				if want.ID != got.ID {
					t.Errorf("expected Id: %s, got Id: %s", want.ID, got.ID)
				}

				if want.Type != got.Type {
					t.Errorf("expected Type: %s, got Type: %s", want.Type, got.Type)
				}

				if diff := compare(want.Config, got.Config); diff != "" {
					t.Errorf("expected Config: \n%s got Config: \n%s diff:\n%s", want.Config, got.Config, diff)
				}
			}
		})
	}
}

func toJSON(i interface{}) []byte {
	switch v := i.(type) {
	case string:
		return []byte(v)
	}

	b, _ := json.Marshal(i)
	return b
}

func compare(a interface{}, b interface{}) string {

	patch, err := jsonpatch.CreateMergePatch(toJSON(a), toJSON(b))
	if err != nil {
		return err.Error()
	}

	if len(patch) <= 2 { // no patch or empty array
		return ""
	}

	return string(patch)
}
