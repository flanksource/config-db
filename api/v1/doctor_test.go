package v1

import (
	"fmt"

	clickyapi "github.com/flanksource/clicky/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DoctorResults", func() {
	It("reports a failed check without treating skipped checks as failures", func() {
		results := DoctorResults{
			{Status: DoctorStatusPass},
			{Status: DoctorStatusSkip},
			{Status: DoctorStatusFail},
		}

		Expect(results.Failed()).To(BeTrue())
		Expect(results.FailureCount()).To(Equal(1))
	})

	It("provides stable rich table fields", func() {
		result := DoctorResult{
			Scraper:       "github",
			Config:        "public-metadata",
			Resource:      "acme/widgets",
			Operation:     "repository metadata",
			Required:      []string{"github:metadata=read"},
			Granted:       []string{"oauth:repo"},
			GrantEvidence: "authenticated request succeeded",
			Status:        DoctorStatusPass,
			Message:       "200 OK",
		}

		columnNames := make([]string, 0, len(result.Columns()))
		for _, column := range result.Columns() {
			columnNames = append(columnNames, column.Name)
		}

		Expect(columnNames).To(Equal([]string{
			"Status",
			"Scraper",
			"Config",
			"Resource",
			"Operation",
			"Required",
			"Granted",
			"Evidence",
			"Message",
		}))
		Expect(result.Row()).To(HaveKeyWithValue("Required", "github:metadata=read"))
		Expect(result.Row()).To(HaveKeyWithValue("Granted", "oauth:repo"))
		Expect(result.Row()["Status"]).To(Satisfy(func(value any) bool {
			_, ok := value.(clickyapi.Textable)
			return ok && fmt.Sprint(value) != ""
		}))
	})
})
