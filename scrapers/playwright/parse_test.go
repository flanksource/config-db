package playwright

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/config-db/api"
	dutyContext "github.com/flanksource/duty/context"
)

func TestPlaywright(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Playwright Suite")
}

var _ = Describe("parseOutput", func() {
	var ctx api.ScrapeContext
	var config v1.Playwright

	BeforeEach(func() {
		ctx = api.NewScrapeContext(dutyContext.New())
		config = v1.Playwright{}
		config.BaseScraper.Type = "Test::Type"
	})

	It("should parse raw JSON array", func() {
		results, err := parseOutput(ctx, config, `[{"id":"1"},{"id":"2"}]`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
	})

	It("should parse raw JSON object", func() {
		results, err := parseOutput(ctx, config, `{"id":"1","name":"foo"}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
	})

	It("should parse wrapped {data, changes}", func() {
		results, err := parseOutput(ctx, config, `{"data":[{"id":"1"}],"changes":[{"change_type":"Screenshot","config_id":"abc-123","summary":"test"}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Changes).To(HaveLen(1))
		Expect(results[0].Changes[0].ChangeType).To(Equal("Screenshot"))
		Expect(results[0].Changes[0].ExternalID).To(Equal("abc-123"))
	})

	It("should parse null data with changes only", func() {
		results, err := parseOutput(ctx, config, `{"data":null,"changes":[{"change_type":"Backup","external_id":"db-001","severity":"info"}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Changes[0].ExternalID).To(Equal("db-001"))
		Expect(results[0].Changes[0].ScraperID).To(Equal("all"))
	})

	It("should route non-UUID config_id to external_id", func() {
		results, err := parseOutput(ctx, config, `{"data":null,"changes":[{"change_type":"Test","config_id":"my-instance"}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results[0].Changes[0].ConfigID).To(BeEmpty())
		Expect(results[0].Changes[0].ExternalID).To(Equal("my-instance"))
	})

	It("should keep UUID config_id as config_id", func() {
		results, err := parseOutput(ctx, config, `{"data":null,"changes":[{"change_type":"Test","config_id":"550e8400-e29b-41d4-a716-446655440000"}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results[0].Changes[0].ConfigID).To(Equal("550e8400-e29b-41d4-a716-446655440000"))
	})

	It("should handle empty output", func() {
		results, err := parseOutput(ctx, config, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeNil())
	})

	It("should handle plain text", func() {
		results, err := parseOutput(ctx, config, "not json")
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
	})

	It("should parse artifacts in changes", func() {
		results, err := parseOutput(ctx, config, `{"data":null,"changes":[{"change_type":"S","details":{"artifacts":[{"name":"t.png","sha":"abc","size":1024}]}}]}`)
		Expect(err).ToNot(HaveOccurred())
		artifacts := results[0].Changes[0].Details["artifacts"].([]any)
		Expect(artifacts).To(HaveLen(1))
		Expect(artifacts[0].(map[string]any)["name"]).To(Equal("t.png"))
	})
})
