package cmd

import (
	"encoding/json"
	"errors"

	clickyapi "github.com/flanksource/clicky/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("doctor target", func() {
	It("prefers an existing local file", func() {
		target, err := classifyDoctorTarget("../README.md")

		Expect(err).NotTo(HaveOccurred())
		Expect(target.kind).To(Equal(doctorTargetFile))
		Expect(target.value).To(Equal("../README.md"))
	})

	It("accepts a persisted scraper UUID", func() {
		id := uuid.MustParse("2cf4c54f-b971-401a-a922-d35c0a630b8e")

		target, err := classifyDoctorTarget(id.String())

		Expect(err).NotTo(HaveOccurred())
		Expect(target.kind).To(Equal(doctorTargetID))
		Expect(target.id).To(Equal(id))
	})

	It("rejects a missing non-UUID target", func() {
		_, err := classifyDoctorTarget("missing-doctor-config.yaml")

		Expect(err).To(MatchError(ContainSubstring("existing file or scraper UUID")))
	})

	It("requires exactly one target", func() {
		_, err := runDoctor(DoctorOptions{})

		Expect(err).To(MatchError("doctor requires exactly one scraper file or scraper UUID"))
	})
})

var _ = Describe("doctor failure output", func() {
	It("remains rich-renderable and marshals as the complete result list", func() {
		results := v1.DoctorResults{{
			Scraper:   "github",
			Operation: "repository metadata",
			Status:    v1.DoctorStatusFail,
		}}
		failure := doctorFailure{
			results: results,
			err:     errors.New("permission denied"),
		}

		Expect(clickyapi.TryTypedValue(failure)).NotTo(BeNil())
		actual, err := json.Marshal(failure)
		Expect(err).NotTo(HaveOccurred())
		expected, err := json.Marshal(results)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(MatchJSON(expected))
	})
})
