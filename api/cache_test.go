package api

import (
	gocontext "context"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	dutycontext "github.com/flanksource/duty/context"
)

var _ = Describe("TempCache", func() {
	var (
		ctx       ScrapeContext
		scraperID uuid.UUID
	)

	BeforeEach(func() {
		scraperID = uuid.New()
		scrapeConfig := &v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				UID:       types.UID(scraperID.String()),
				Name:      "aws",
				Namespace: "default",
			},
		}
		ctx = NewScrapeContext(dutycontext.NewContext(gocontext.Background())).
			WithScrapeConfig(scrapeConfig)
		Expect(ctx.ScraperID()).To(Equal(scraperID.String()))
	})

	It("finds an aliased config when the lookup uses ScraperID=all", func() {
		efsARN := "arn:aws:elasticfilesystem:eu-west-1:111111111111:file-system/fs-0f6dafb1128f44e71"
		efs := models.ConfigItem{
			ID:         "efs-config-id",
			Type:       "AWS::EFS::FileSystem",
			ExternalID: []string{"fs-0f6dafb1128f44e71", v1.NormalizeExternalID(efsARN)},
			ScraperID:  lo.ToPtr(scraperID),
		}
		ctx.TempCache().Insert(efs)

		// AWS Backup emits changes with ScraperID=all; the EFS config is
		// cached under the concrete scraper UUID. The fallback should
		// still locate the EFS config.
		lookup := v1.ExternalID{
			ConfigType: "AWS::EFS::FileSystem",
			ExternalID: efsARN,
			ScraperID:  "all",
		}

		id, err := ctx.TempCache().FindExternalID(ctx, lookup)
		Expect(err).ToNot(HaveOccurred())
		Expect(id).To(Equal("efs-config-id"))
	})

	It("invalidates a wildcard miss when inserting an alias", func() {
		efsARN := "arn:aws:elasticfilesystem:eu-west-1:111111111111:file-system/fs-0f6dafb1128f44e71"
		lookup := v1.ExternalID{
			ConfigType: "AWS::EFS::FileSystem",
			ExternalID: efsARN,
			ScraperID:  "all",
		}

		item, err := ctx.TempCache().Find(ctx, lookup)
		Expect(err).ToNot(HaveOccurred())
		Expect(item).To(BeNil())
		_, cached := ctx.TempCache().notFound.Load(lookup.Key())
		Expect(cached).To(BeTrue())

		ctx.TempCache().Insert(models.ConfigItem{
			ID:         "efs-config-id",
			Type:       "AWS::EFS::FileSystem",
			ExternalID: []string{v1.NormalizeExternalID(efsARN)},
			ScraperID:  lo.ToPtr(scraperID),
		})

		item, err = ctx.TempCache().Find(ctx, lookup)
		Expect(err).ToNot(HaveOccurred())
		Expect(item).ToNot(BeNil())
		Expect(item.ID).To(Equal("efs-config-id"))
	})

	It("returns not found for an uncached external ID without a database", func() {
		lookup := v1.ExternalID{
			ConfigType: "AWS::EFS::FileSystem",
			ExternalID: "missing-file-system",
		}

		item, err := ctx.TempCache().Find(ctx, lookup)
		Expect(err).ToNot(HaveOccurred())
		Expect(item).To(BeNil())

		lookup.ScraperID = ctx.ScraperID()
		_, cached := ctx.TempCache().notFound.Load(lookup.Key())
		Expect(cached).To(BeTrue())
	})

	It("resolves an external ID inserted after a standalone miss", func() {
		externalID := "fs-0f6dafb1128f44e71"
		item, err := ctx.TempCache().Get(ctx, externalID)
		Expect(err).ToNot(HaveOccurred())
		Expect(item).To(BeNil())
		_, cached := ctx.TempCache().notFound.Load(externalID)
		Expect(cached).To(BeFalse())

		lookup := v1.ExternalID{
			ConfigType: "AWS::EFS::FileSystem",
			ExternalID: externalID,
		}
		item, err = ctx.TempCache().Find(ctx, lookup)
		Expect(err).ToNot(HaveOccurred())
		Expect(item).To(BeNil())

		ctx.TempCache().Insert(models.ConfigItem{
			ID:         uuid.NewString(),
			Type:       lookup.ConfigType,
			ExternalID: []string{externalID},
			ScraperID:  lo.ToPtr(scraperID),
		})

		item, err = ctx.TempCache().Find(ctx, lookup)
		Expect(err).ToNot(HaveOccurred())
		Expect(item).ToNot(BeNil())
		Expect(item.ExternalID).To(ContainElement(externalID))
	})

	It("returns not found for an uncached config ID without a database", func() {
		id := uuid.NewString()

		item, err := ctx.TempCache().Get(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(item).To(BeNil())

		_, cached := ctx.TempCache().notFound.Load(id)
		Expect(cached).To(BeTrue())
	})
})
