package scrapers

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("External users e2e test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-external-users")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users, groups, and roles", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external users to the database with aliases", func() {
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2))

		userNames := lo.Map(users, func(u dutymodels.ExternalUser, _ int) string { return u.Name })
		Expect(userNames).To(ContainElements("John Doe", "Service Bot"))

		for _, user := range users {
			switch user.Name {
			case "John Doe":
				Expect(user.Aliases).To(ContainElements("john-doe", "jdoe@example.com"))
			case "Service Bot":
				Expect(user.Aliases).To(ContainElements("service-bot", "bot-001"))
			}
		}
	})

	It("should have saved external groups to the database", func() {
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))

		groupNames := lo.Map(groups, func(g dutymodels.ExternalGroup, _ int) string { return g.Name })
		Expect(groupNames).To(ContainElements("Administrators", "Developers"))
	})

	It("should have saved external roles to the database", func() {
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(2))

		roleNames := lo.Map(roles, func(r dutymodels.ExternalRole, _ int) string { return r.Name })
		Expect(roleNames).To(ContainElements("Admin", "Reader"))
	})

	It("should upsert external users by alias on second scrape (not create duplicates)", func() {
		var usersBefore []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&usersBefore).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(usersBefore).To(HaveLen(2))

		userIDsBefore := lo.Map(usersBefore, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })

		db.ExternalUserCache.Flush()

		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var usersAfter []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&usersAfter).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(usersAfter).To(HaveLen(2))

		userIDsAfter := lo.Map(usersAfter, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })
		Expect(userIDsAfter).To(ConsistOf(userIDsBefore))
	})

	It("should use cache for external user lookup on subsequent scrapes", func() {
		db.ExternalUserCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		johnDoeID, found := db.ExternalUserCache.Get("john-doe")
		Expect(found).To(BeTrue())
		Expect(johnDoeID).NotTo(Equal(uuid.Nil))

		jdoeID, found := db.ExternalUserCache.Get("jdoe@example.com")
		Expect(found).To(BeTrue())
		Expect(jdoeID).To(Equal(johnDoeID))

		serviceBotID, found := db.ExternalUserCache.Get("service-bot")
		Expect(found).To(BeTrue())
		Expect(serviceBotID).NotTo(Equal(uuid.Nil))

		bot001ID, found := db.ExternalUserCache.Get("bot-001")
		Expect(found).To(BeTrue())
		Expect(bot001ID).To(Equal(serviceBotID))
	})
})

var _ = Describe("Stale external entities deletion test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper
	var initialUserIDs []uuid.UUID

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-config-access")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		err := DefaultContext.DB().Unscoped().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		err = DefaultContext.DB().Unscoped().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users initially", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var users []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Where("deleted_at IS NULL").Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2))

		initialUserIDs = lo.Map(users, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })
	})

	It("should create an additional external user directly in DB", func() {
		staleUser := dutymodels.ExternalUser{
			Name:      "Stale User",
			AccountID: "org-456",
			UserType:  "human",
			ScraperID: scraperModel.ID,
			Aliases:   []string{"stale-user"},
		}
		err := DefaultContext.DB().Create(&staleUser).Error
		Expect(err).NotTo(HaveOccurred())

		var users []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Where("deleted_at IS NULL").Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(3))
	})

	It("should soft delete stale external user on next scrape", func() {
		db.ExternalUserCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var activeUsers []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Where("deleted_at IS NULL").Find(&activeUsers).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(activeUsers).To(HaveLen(2))

		activeUserIDs := lo.Map(activeUsers, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })
		Expect(activeUserIDs).To(ConsistOf(initialUserIDs))

		var deletedUsers []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Where("deleted_at IS NOT NULL").Find(&deletedUsers).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(deletedUsers).To(HaveLen(1))
		Expect(deletedUsers[0].Name).To(Equal("Stale User"))
	})
})

var _ = Describe("External roles with aliases e2e test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-external-users")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external roles with aliases", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external roles to the database with aliases", func() {
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(2))

		roleNames := lo.Map(roles, func(r dutymodels.ExternalRole, _ int) string { return r.Name })
		Expect(roleNames).To(ContainElements("Admin", "Reader"))

		for _, role := range roles {
			switch role.Name {
			case "Admin":
				Expect(role.Aliases).To(ContainElements("admin-role", "administrator"))
			case "Reader":
				Expect(role.Aliases).To(ContainElements("reader-role", "read-only"))
			}
		}
	})

	It("should upsert external roles by alias on second scrape (not create duplicates)", func() {
		var rolesBefore []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&rolesBefore).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(rolesBefore).To(HaveLen(2))

		roleIDsBefore := lo.Map(rolesBefore, func(r dutymodels.ExternalRole, _ int) uuid.UUID { return r.ID })

		db.ExternalRoleCache.Flush()

		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var rolesAfter []dutymodels.ExternalRole
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&rolesAfter).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(rolesAfter).To(HaveLen(2))

		roleIDsAfter := lo.Map(rolesAfter, func(r dutymodels.ExternalRole, _ int) uuid.UUID { return r.ID })
		Expect(roleIDsAfter).To(ConsistOf(roleIDsBefore))
	})

	It("should use cache for external role lookup on subsequent scrapes", func() {
		db.ExternalRoleCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		adminID, found := db.ExternalRoleCache.Get("admin-role")
		Expect(found).To(BeTrue())
		Expect(adminID).NotTo(Equal(uuid.Nil))

		administratorID, found := db.ExternalRoleCache.Get("administrator")
		Expect(found).To(BeTrue())
		Expect(administratorID).To(Equal(adminID))

		readerID, found := db.ExternalRoleCache.Get("reader-role")
		Expect(found).To(BeTrue())
		Expect(readerID).NotTo(Equal(uuid.Nil))

		readOnlyID, found := db.ExternalRoleCache.Get("read-only")
		Expect(found).To(BeTrue())
		Expect(readOnlyID).To(Equal(readerID))
	})
})

var _ = Describe("External groups with aliases e2e test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-external-users")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external groups with aliases", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external groups to the database with aliases", func() {
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))

		groupNames := lo.Map(groups, func(g dutymodels.ExternalGroup, _ int) string { return g.Name })
		Expect(groupNames).To(ContainElements("Administrators", "Developers"))

		for _, group := range groups {
			switch group.Name {
			case "Administrators":
				Expect(group.Aliases).To(ContainElements("admins-group", "administrators"))
			case "Developers":
				Expect(group.Aliases).To(ContainElements("devs-group", "developers"))
			}
		}
	})

	It("should upsert external groups by alias on second scrape (not create duplicates)", func() {
		var groupsBefore []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groupsBefore).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groupsBefore).To(HaveLen(2))

		groupIDsBefore := lo.Map(groupsBefore, func(g dutymodels.ExternalGroup, _ int) uuid.UUID { return g.ID })

		db.ExternalGroupCache.Flush()

		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var groupsAfter []dutymodels.ExternalGroup
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groupsAfter).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groupsAfter).To(HaveLen(2))

		groupIDsAfter := lo.Map(groupsAfter, func(g dutymodels.ExternalGroup, _ int) uuid.UUID { return g.ID })
		Expect(groupIDsAfter).To(ConsistOf(groupIDsBefore))
	})

	It("should use cache for external group lookup on subsequent scrapes", func() {
		db.ExternalGroupCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		adminsID, found := db.ExternalGroupCache.Get("admins-group")
		Expect(found).To(BeTrue())
		Expect(adminsID).NotTo(Equal(uuid.Nil))

		administratorsID, found := db.ExternalGroupCache.Get("administrators")
		Expect(found).To(BeTrue())
		Expect(administratorsID).To(Equal(adminsID))

		devsID, found := db.ExternalGroupCache.Get("devs-group")
		Expect(found).To(BeTrue())
		Expect(devsID).NotTo(Equal(uuid.Nil))

		developersID, found := db.ExternalGroupCache.Get("developers")
		Expect(found).To(BeTrue())
		Expect(developersID).To(Equal(devsID))
	})
})
