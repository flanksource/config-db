package github

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FlexTime", func() {
	DescribeTable("UnmarshalJSON",
		func(input string, expectErr bool, expectedYear int) {
			var ft FlexTime
			err := json.Unmarshal([]byte(input), &ft)
			if expectErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).ToNot(HaveOccurred())
				Expect(ft.Year()).To(Equal(expectedYear))
			}
		},
		Entry("RFC3339 with time", `"2026-03-26T00:43:47Z"`, false, 2026),
		Entry("date-only", `"2024-03-19"`, false, 2024),
		Entry("invalid value", `"not-a-date"`, true, 0),
	)

	It("parses a full ScorecardResponse with date-only date field", func() {
		raw := `{
			"date": "2024-03-19",
			"repo": {"name": "github.com/flanksource/flanksource-ui", "commit": "abc123"},
			"scorecard": {"version": "v4.10.2", "commit": "def456"},
			"score": 5.5,
			"checks": []
		}`
		var resp ScorecardResponse
		Expect(json.Unmarshal([]byte(raw), &resp)).To(Succeed())
		Expect(resp.Date.Year()).To(Equal(2024))
		Expect(resp.Date.Month()).To(Equal(time.March))
		Expect(resp.Date.Day()).To(Equal(19))
	})

	It("parses a full ScorecardResponse with RFC3339 date field", func() {
		raw := `{
			"date": "2026-03-26T00:43:47Z",
			"repo": {"name": "github.com/flanksource/duty", "commit": "abc123"},
			"scorecard": {"version": "v4.13.1", "commit": "def456"},
			"score": 6.5,
			"checks": []
		}`
		var resp ScorecardResponse
		Expect(json.Unmarshal([]byte(raw), &resp)).To(Succeed())
		Expect(resp.Date.Year()).To(Equal(2026))
		Expect(resp.Date.Hour()).To(Equal(0))
		Expect(resp.Date.Minute()).To(Equal(43))
	})
})
