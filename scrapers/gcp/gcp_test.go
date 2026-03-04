package gcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloud.google.com/go/asset/apiv1/assetpb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/structpb"
)

type testFixture struct {
	Expectation struct {
		Zone   string `json:"zone"`
		Region string `json:"region"`
	} `json:"expectation"`
}

func TestGCP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GCP Suite")
}

var _ = Describe("parseResourceData", func() {
	It("extracts zone and region from testdata fixtures", func() {
		files, err := os.ReadDir("testdata")
		Expect(err).ToNot(HaveOccurred())

		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".json") {
				continue
			}

			By(file.Name())
			filePath := filepath.Join("testdata", file.Name())
			fileContent, err := os.ReadFile(filePath)
			Expect(err).ToNot(HaveOccurred())

			var resourceData map[string]any
			Expect(json.Unmarshal(fileContent, &resourceData)).To(Succeed())

			var fixture testFixture
			Expect(json.Unmarshal(fileContent, &fixture)).To(Succeed())

			delete(resourceData, "expectation")

			data, err := structpb.NewStruct(resourceData)
			Expect(err).ToNot(HaveOccurred())

			asset := &assetpb.Asset{Resource: &assetpb.Resource{Data: data}}
			result := parseResourceData(asset)
			Expect(result.Zone).To(Equal(fixture.Expectation.Zone))
			Expect(result.Region).To(Equal(fixture.Expectation.Region))
		}
	})
})
