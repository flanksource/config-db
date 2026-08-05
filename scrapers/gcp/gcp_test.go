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

var _ = Describe("parseResourceData aliases", func() {
	assetWith := func(assetType string, fields map[string]any) *assetpb.Asset {
		data, err := structpb.NewStruct(fields)
		Expect(err).ToNot(HaveOccurred())
		return &assetpb.Asset{AssetType: assetType, Resource: &assetpb.Resource{Data: data}}
	}

	It("aliases a service account by email so it matches its IAM principal", func() {
		rd := parseResourceData(assetWith(serviceAccountAssetType, map[string]any{
			"name":  "projects/gcp-proj-1/serviceAccounts/sa-etl@gcp-proj-1.iam.gserviceaccount.com",
			"email": "sa-etl@gcp-proj-1.iam.gserviceaccount.com",
		}))

		Expect(rd.Aliases).To(ContainElement("sa-etl@gcp-proj-1.iam.gserviceaccount.com"))
	})

	It("does not alias other asset types by email", func() {
		rd := parseResourceData(assetWith("storage.googleapis.com/Bucket", map[string]any{
			"name":  "my-bucket",
			"email": "owner@example.com",
		}))

		Expect(rd.Aliases).ToNot(ContainElement("owner@example.com"))
	})

	It("emits no empty aliases for an asset without a self link", func() {
		rd := parseResourceData(assetWith(serviceAccountAssetType, map[string]any{
			"name":  "projects/gcp-proj-1/serviceAccounts/sa-etl@gcp-proj-1.iam.gserviceaccount.com",
			"email": "sa-etl@gcp-proj-1.iam.gserviceaccount.com",
		}))

		Expect(rd.Aliases).ToNot(ContainElement(""))
	})

	It("keeps the self link aliases for assets that have one", func() {
		rd := parseResourceData(assetWith("compute.googleapis.com/Instance", map[string]any{
			"name":     "vm-1",
			"selfLink": "https://www.googleapis.com/compute/v1/projects/p/zones/us-east1-b/instances/vm-1",
		}))

		Expect(rd.Aliases).To(ContainElements(
			"https://www.googleapis.com/compute/v1/projects/p/zones/us-east1-b/instances/vm-1",
			"projects/p/zones/us-east1-b/instances/vm-1",
		))
	})
})

var _ = Describe("parseResourceData", func() {
	It("extracts zone and region from testdata fixtures", func() {
		files, err := os.ReadDir("testdata")
		Expect(err).ToNot(HaveOccurred())

		processed := 0
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
			processed++
		}
		Expect(processed).To(BeNumerically(">", 0), "expected at least one .json fixture in testdata/")
	})
})
