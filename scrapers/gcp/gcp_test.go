package gcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloud.google.com/go/asset/apiv1/assetpb"
	"github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/structpb"
)

type testFixture struct {
	Expectation struct {
		Zone   string `json:"zone"`
		Region string `json:"region"`
	} `json:"expectation"`
}

func TestParseResourceData(t *testing.T) {
	g := gomega.NewWithT(t)

	files, err := os.ReadDir("testdata")
	g.Expect(err).To(gomega.BeNil())

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		t.Run(file.Name(), func(t *testing.T) {
			filePath := filepath.Join("testdata", file.Name())
			fileContent, err := os.ReadFile(filePath)
			g.Expect(err).To(gomega.BeNil())

			var resourceData map[string]any
			err = json.Unmarshal(fileContent, &resourceData)
			g.Expect(err).To(gomega.BeNil())

			var fixture testFixture
			err = json.Unmarshal(fileContent, &fixture)
			g.Expect(err).To(gomega.BeNil())

			delete(resourceData, "expectation")

			data, err := structpb.NewStruct(resourceData)
			g.Expect(err).To(gomega.BeNil())

			asset := &assetpb.Asset{
				Resource: &assetpb.Resource{
					Data: data,
				},
			}
			result := parseResourceData(asset)

			g.Expect(result.Zone).To(gomega.Equal(fixture.Expectation.Zone), "zone mismatch")
			g.Expect(result.Region).To(gomega.Equal(fixture.Expectation.Region), "region mismatch")
		})
	}
}
