package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("Slack spec", func() {
	It("scrapes members unless they are explicitly disabled", func() {
		Expect(Slack{}.ScrapeMembers()).To(BeTrue())
		Expect(Slack{Members: lo.ToPtr(true)}.ScrapeMembers()).To(BeTrue())
		Expect(Slack{Members: lo.ToPtr(false)}.ScrapeMembers()).To(BeFalse())
	})

	It("parses a channel-only spec that declares no change extraction rules", func() {
		configs, err := ParseConfigs("../../fixtures/slack-channels.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(configs).To(HaveLen(1))

		slack := configs[0].Spec.Slack
		Expect(slack).To(HaveLen(1))
		Expect(slack[0].Rules).To(BeEmpty())
		Expect(slack[0].ScrapeMembers()).To(BeTrue())
		Expect(slack[0].Messages).To(BeFalse())
	})

	It("refuses to load a spec that still sets the removed channels filter", func() {
		// Loading it would discard the filter and scrape every channel, which is
		// the opposite of what the config asks for.
		_, err := ParseConfigs("testdata/slack-removed-channels.yaml")
		Expect(err).To(MatchError(ContainSubstring("spec.slack[0].channels has been removed")))
	})

	It("only ingests messages when the spec opts in", func() {
		configs, err := ParseConfigs("../../fixtures/slack.yaml")
		Expect(err).NotTo(HaveOccurred())

		slack := configs[0].Spec.Slack
		Expect(slack).To(HaveLen(1))
		Expect(slack[0].Messages).To(BeTrue())
		Expect(slack[0].Rules).To(HaveLen(1))
	})
})
