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
						Paths: []string{
							"../fixtures/config*.json",
							"../fixtures/test*.json",
						},
					},
					{
						ID:   "metadata.name",
						Type: "kind",
						Paths: []string{
							"fixtures/minimal/http_pass.yaml",
						},
						URL: "github.com/flanksource/canary-checker",
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
				{
					Config: `{
						"apiVersion": "canaries.flanksource.com/v1",
						"kind": "Canary",
						"metadata": {
						  "name": "http-pass"
						},
						"spec": {
						  "interval": 30,
						  "http": [
							{
							  "endpoint": "http://status.savanttools.com/?code=200",
							  "thresholdMillis": 3000,
							  "responseCodes": [
								201,
								200,
								301
							  ],
							  "responseContent": "",
							  "maxSSLExpiry": 7,
							  "test": {
								"expr": "code == 200"
							  }
							},
							{
							  "endpoint": "http://status.savanttools.com/?code=404",
							  "thresholdMillis": 3000,
							  "responseCodes": [
								404
							  ],
							  "responseContent": "",
							  "maxSSLExpiry": 7
							},
							{
							  "endpoint": "http://status.savanttools.com/?code=500",
							  "thresholdMillis": 3000,
							  "responseCodes": [
								500
							  ],
							  "responseContent": "",
							  "maxSSLExpiry": 7
							}
						  ]
						}
					  }`,
					Type: "Canary",
					ID:   "http-pass",
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
