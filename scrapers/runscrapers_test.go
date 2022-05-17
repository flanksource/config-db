package scrapers

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/flanksource/confighub/api/v1"
	fs "github.com/flanksource/confighub/filesystem"
)

func TestRun(t *testing.T) {

	testTable := []struct {
		ctx            v1.ScrapeContext
		manager        v1.Manager
		config         v1.ConfigScraper
		expectedResult []v1.ScrapeResult
	}{
		{
			manager: v1.Manager{
				Finder: fs.NewFileFinder(),
			},
			config: v1.ConfigScraper{
				File: []v1.File{
					{
						ID:   "Config.InstanceId",
						Type: "Config.InstanceType",
						Glob: []string{
							"../fixtures/config*.json",
							"../fixtures/test*.json",
						},
					},
				},
			},
			expectedResult: []v1.ScrapeResult{
				{
					Config: `{"Config": {"InstanceId": "instance_id_1","InstanceType": "instance_type_1"}}`,
					Type:   "instance_type_1",
					ID:     "instance_id_1",
				},
				{
					Config: `{"Config": {"InstanceId": "instance_id_2","InstanceType": "instance_type_2"}}`,
					Type:   "instance_type_2",
					ID:     "instance_id_2",
				},
			},
		},
	}

	for _, tc := range testTable {
		results, err := Run(tc.ctx, tc.manager, tc.config)

		if err != nil {
			t.Errorf("Unexpected error:%s", err.Error())
		}

		if len(results) != len(tc.expectedResult) {
			t.Errorf("expected %d results, got: %d", len(tc.expectedResult), len(results))
		}

		for i := 0; i < len(results); i++ {
			want := tc.expectedResult[i]
			got := results[i]

			if want.ID != got.ID {
				t.Errorf("expected Id: %s, got Id: %s", want.ID, got.ID)
			}

			if want.Type != got.Type {
				t.Errorf("expected Type: %s, got Type: %s", want.Type, got.Type)
			}

			wantConfig := map[string]interface{}{}

			gotConfig := map[string]interface{}{}

			if err := json.Unmarshal([]byte(want.Config.(string)), &wantConfig); err != nil {
				t.Error("failed decode wanted config body: ", err.Error())
			}
			if err := json.Unmarshal([]byte(got.Config.(string)), &gotConfig); err != nil {
				t.Error("failed decode got config body: ", err.Error())
			}

			if !reflect.DeepEqual(wantConfig, gotConfig) {
				t.Errorf("expected Config: %v, got Config: %v", wantConfig, gotConfig)
			}

		}
	}

}
