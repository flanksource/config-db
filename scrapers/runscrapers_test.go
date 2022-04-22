package scrapers

import (
	"bytes"
	"encoding/json"
	"testing"

	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/matchers"
)

func TestRun(t *testing.T) {

	testTable := []struct {
		ctx            v1.ScrapeContext
		config         v1.ConfigScraper
		expectedResult []v1.ScrapeResult
	}{
		{
			ctx: v1.ScrapeContext{
				Matcher: matchers.NewMock(map[string]string{
					"config_test_1.json": `{"Config": {"InstanceId": "instance_id_1","InstanceType": "instance_type_1"}}`,
					"test_2_config.json": `{"Config": {"InstanceId": "instance_id_2","InstanceType": "instance_type_2"}}`,
				}),
			},
			config: v1.ConfigScraper{
				File: []v1.File{
					{
						ID:   "Config.InstanceId",
						Type: "Config.InstanceType",
						Glob: []string{
							"config*.json",
							"test*.json",
						},
					},
				},
			},
			expectedResult: []v1.ScrapeResult{
				{
					Config: `{"Config": {"InstanceId": "instance_id_1","InstanceType": "instance_type_1"}}`,
					Type:   "instance_type_1",
					Id:     "instance_id_1",
				},
				{
					Config: `{"Config": {"InstanceId": "instance_id_2","InstanceType": "instance_type_2"}}`,
					Type:   "instance_type_2",
					Id:     "instance_id_2",
				},
			},
		},
	}

	for _, tc := range testTable {
		results, err := Run(tc.ctx, tc.config)

		if err != nil {
			t.Errorf("Unexpected error:%s", err.Error())
		}

		gotBytes, _ := json.Marshal(results)
		wantBytes, _ := json.Marshal(tc.expectedResult)

		if bytes.Compare(gotBytes, wantBytes) != 0 {
			t.Errorf("expected: %v, got: %v", string(wantBytes), string(gotBytes))
		}
	}

}
