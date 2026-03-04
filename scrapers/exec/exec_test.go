package exec

import (
	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseOutput", func() {
	var config v1.Exec

	BeforeEach(func() {
		config = v1.Exec{}
	})

	It("should parse a JSON object into a single result", func() {
		stdout := `{"id": "123", "name": "test", "type": "TestType"}`
		results := parseOutput(config, stdout)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Error).To(BeNil())
		configMap, ok := results[0].Config.(map[string]any)
		Expect(ok).To(BeTrue(), "config should be a map")
		Expect(configMap["id"]).To(Equal("123"))
		Expect(configMap["name"]).To(Equal("test"))
		Expect(configMap["type"]).To(Equal("TestType"))
	})

	It("should parse a JSON array into multiple results", func() {
		stdout := `[{"id": "1", "name": "first"}, {"id": "2", "name": "second"}]`
		results := parseOutput(config, stdout)
		Expect(results).To(HaveLen(2))
		for _, result := range results {
			Expect(result.Error).To(BeNil())
			Expect(result.Config).ToNot(BeNil(), "each result should have a non-nil config")
		}
		first := results[0].Config.(map[string]any)
		Expect(first["id"]).To(Equal("1"))
		Expect(first["name"]).To(Equal("first"))
		second := results[1].Config.(map[string]any)
		Expect(second["id"]).To(Equal("2"))
		Expect(second["name"]).To(Equal("second"))
	})

	It("should parse YAML into a single result", func() {
		stdout := `
id: "123"
name: "test"
type: "TestType"
`
		results := parseOutput(config, stdout)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Error).To(BeNil())
	})

	It("should return no results for empty output", func() {
		results := parseOutput(config, "")
		Expect(results).To(BeEmpty())
	})

	It("should treat plain text as YAML string", func() {
		stdout := `This is plain text output that is not JSON or YAML`
		results := parseOutput(config, stdout)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Error).To(BeNil())
		Expect(results[0].Config).ToNot(BeNil())
		configStr, ok := results[0].Config.(string)
		Expect(ok).To(BeTrue(), "plain text config should be a string")
		Expect(configStr).To(ContainSubstring("This is plain text output that is not JSON or YAML"))
	})
})
