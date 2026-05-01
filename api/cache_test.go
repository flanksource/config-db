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

var _ = Describe("TempCache ScraperID=all fallback", func() {
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
})
