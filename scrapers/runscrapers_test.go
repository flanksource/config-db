package scrapers

import (
	"encoding/json"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
	v1 "github.com/flanksource/config-db/api/v1"
	fs "github.com/flanksource/config-db/filesystem"
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
						BaseScraper: v1.BaseScraper{
							ID:   "$.Config.InstanceId",
							Type: "$.Config.InstanceType",
						},

						Paths: []string{
							"../fixtures/config*.json",
							"../fixtures/test*.json",
						},
					},
					{
						BaseScraper: v1.BaseScraper{
							ID:   "$.metadata.name",
							Type: "$.kind",
						},

						Paths: []string{
							"fixtures/minimal/http_pass_single.yaml",
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
                   		"name": "http-pass-single",
											"labels": {
												"canary": "http"
											}
                   	},
                   	"spec": {
                   		"http": [
                   			{
                   				"endpoint": "http://status.savanttools.com/?code=200",
                   				"maxSSLExpiry": 7,
                   				"name": "sample-check",
                   				"responseCodes": [
                   					201,
                   					200,
                   					301
                   				],
                   				"responseContent": "",
                   				"test": {
                   					"expr": "code == 200"
                   				},
                   				"thresholdMillis": 3000
                   			}
                   		],
                   		"interval": 30
                   	}
                   }`,
					Type: "Canary",
					ID:   "http-pass-single",
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

		for i := 0; i < len(tc.expectedResult); i++ {
			want := tc.expectedResult[i]
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
	}
}

// nolint:gosimple
func toJSON(v interface{}) []byte {
	switch v.(type) {
	case string:
		s := v.(string)
		return []byte(s)
	}
	b, _ := json.Marshal(v)
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
