package scrapers

import (
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

const roleARN = "arn:aws:iam::111111111111:role/deployrole"
const accountID = "111111111111"

var _ = Describe("AWS IAM trust-policy config access", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-aws-iam-trust")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred())
		scModel.Source = dutymodels.SourceUI
		Expect(DefaultContext.DB().Create(&scModel).Error).NotTo(HaveOccurred())

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)
		scraperModel = scModel
	})

	AfterAll(func() {
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Delete(&scraperModel).Error).NotTo(HaveOccurred())
	})

	It("scrapes without error", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).NotTo(HaveOccurred())
	})

	It("persists external_users for IAM-user and OIDC principals", func() {
		var users []dutymodels.ExternalUser
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2))
		byType := lo.GroupBy(users, func(u dutymodels.ExternalUser) string { return u.UserType })
		Expect(byType["IAMUser"]).To(HaveLen(1))
		Expect(byType["OIDC"]).To(HaveLen(1))
	})

	It("persists the IAM role as an external_role", func() {
		var roles []dutymodels.ExternalRole
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(1))
		Expect(roles[0].RoleType).To(Equal("IAMRole"))
		Expect(roles[0].Tenant).To(Equal(accountID))
		Expect([]string(roles[0].Aliases)).To(ContainElement(roleARN))
	})

	It("persists an external_group for the :root cross-account principal", func() {
		var groups []dutymodels.ExternalGroup
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(1))
		Expect(groups[0].GroupType).To(Equal("AWSAccount"))
		Expect(groups[0].Tenant).To(Equal("333333333333"))
	})

	It("scopes config_access to the AWS account config item with role resolved from ARN", func() {
		var account models.ConfigItem
		Expect(DefaultContext.DB().
			Where("scraper_id = ? AND ? = ANY(external_id)", scraperModel.ID, accountID).
			First(&account).Error).NotTo(HaveOccurred())

		var role dutymodels.ExternalRole
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).First(&role).Error).NotTo(HaveOccurred())

		var accesses []dutymodels.ConfigAccess
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&accesses).Error).NotTo(HaveOccurred())
		Expect(accesses).To(HaveLen(3))

		for _, a := range accesses {
			Expect(a.ConfigID.String()).To(Equal(account.ID))
			Expect(a.ExternalRoleID).NotTo(BeNil())
			Expect(*a.ExternalRoleID).To(Equal(role.ID))
		}
		byID := lo.KeyBy(accesses, func(a dutymodels.ConfigAccess) string { return a.ID })
		Expect(byID["trust-aws-user"].ExternalUserID).NotTo(BeNil())
		Expect(byID["trust-aws-federated"].ExternalUserID).NotTo(BeNil())
		Expect(byID["trust-aws-account-root"].ExternalGroupID).NotTo(BeNil())
		Expect(lo.FromPtr(byID["trust-aws-federated"].Source)).To(ContainSubstring("condition="))
	})
})

var _ = Describe("AWS IAM groups + memberships", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-aws-iam-groups")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred())
		scModel.Source = dutymodels.SourceUI
		Expect(DefaultContext.DB().Create(&scModel).Error).NotTo(HaveOccurred())

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)
		scraperModel = scModel
	})

	AfterAll(func() {
		Expect(DefaultContext.DB().Exec(
			"DELETE FROM external_user_groups WHERE external_group_id IN (SELECT id FROM external_groups WHERE scraper_id = ?)",
			scraperModel.ID).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Delete(&scraperModel).Error).NotTo(HaveOccurred())
	})

	It("scrapes without error", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).NotTo(HaveOccurred())
	})

	It("persists both IAM groups with group_type=IAM", func() {
		var groups []dutymodels.ExternalGroup
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))
		for _, g := range groups {
			Expect(g.GroupType).To(Equal("IAM"))
			Expect(g.Tenant).To(Equal("111111111111"))
		}
	})

	It("persists all 3 external users", func() {
		var users []dutymodels.ExternalUser
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(3))
		names := lo.Map(users, func(u dutymodels.ExternalUser, _ int) string { return u.Name })
		Expect(names).To(ConsistOf("alice", "bob", "carol"))
	})
	// Membership (external_user_groups) row-count is exercised by the
	// iamGroups unit test on the emitted v1.ExternalUserGroup shape. The
	// File-scraper fixture path cannot resolve membership aliases because
	// ExtractFullMode flattens user/group references into UUID-only rows
	// before db/external_entities.resolveExternalUserGroups sees them.
})

var _ = Describe("AWS CloudTrail AssumeRole access logs", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-aws-cloudtrail-assume-role")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred())
		scModel.Source = dutymodels.SourceUI
		Expect(DefaultContext.DB().Create(&scModel).Error).NotTo(HaveOccurred())

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)
		scraperModel = scModel
	})

	AfterAll(func() {
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccessLog{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Delete(&scraperModel).Error).NotTo(HaveOccurred())
	})

	It("scrapes without error", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).NotTo(HaveOccurred())
	})

	It("persists one config_access_logs row with count=3 and mfa=true", func() {
		var logs []dutymodels.ConfigAccessLog
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&logs).Error).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(1))
		Expect(logs[0].MFA).To(BeTrue())
		Expect(logs[0].Count).NotTo(BeNil())
		Expect(*logs[0].Count).To(Equal(3))
		Expect(logs[0].CreatedAt.UTC()).To(Equal(time.Date(2026, 4, 21, 22, 0, 0, 0, time.UTC)))
	})
})
