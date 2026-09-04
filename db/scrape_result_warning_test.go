package db

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("scrape result warnings", Ordered, func() {
	var (
		ctx       api.ScrapeContext
		scraperID uuid.UUID
	)

	BeforeAll(func() {
		scraperID = uuid.New()
		ctx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "scrape-result-warning-test",
				Namespace: "default",
				UID:       k8stypes.UID(scraperID.String()),
			},
		})

		Expect(ctx.DB().Create(&models.ConfigScraper{
			ID:        scraperID,
			Name:      "scrape-result-warning-test",
			Namespace: "default",
			Spec:      "{}",
			Source:    models.SourceConfigFile,
		}).Error).ToNot(HaveOccurred())
	})

	AfterAll(func() {
		Expect(ctx.DB().Where("scraper_id = ?", scraperID).Delete(&models.ConfigItem{}).Error).ToNot(HaveOccurred())
		Expect(ctx.DB().Delete(&models.ConfigScraper{}, scraperID).Error).ToNot(HaveOccurred())
	})

	It("surfaces a warning attached to a saved config item in the summary", func() {
		summary, err := saveResults(ctx, []v1.ScrapeResult{{
			ID:          "slack/TWARN",
			Type:        "Slack::Workspace",
			ConfigClass: "Workspace",
			Name:        "warning-workspace",
			Config:      map[string]any{"id": "TWARN"},
			Warnings: []v1.Warning{{
				Error: "failed to list users, members are identified by their slack id: missing_scope",
			}},
		}})

		Expect(err).ToNot(HaveOccurred())
		Expect(summary.Warnings).To(HaveLen(1))
		Expect(summary.Warnings[0].Error).To(ContainSubstring("missing_scope"))
	})
})
