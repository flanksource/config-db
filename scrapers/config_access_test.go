package scrapers

import (
	"errors"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"gorm.io/gorm"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Config access with external_user_aliases test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

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
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users and config access", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external users to the database", func() {
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2))

		userNames := lo.Map(users, func(u dutymodels.ExternalUser, _ int) string { return u.Name })
		Expect(userNames).To(ContainElements("Alice Smith", "Bob Jones"))
	})

	It("should have saved config access with resolved external_user_id from aliases", func() {
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())

		userByName := make(map[string]dutymodels.ExternalUser)
		for _, u := range users {
			userByName[u.Name] = u
		}

		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(2))

		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		access001 := accessByID["access-001"]
		Expect(access001.ExternalUserID).NotTo(BeNil())
		Expect(*access001.ExternalUserID).To(Equal(userByName["Alice Smith"].ID))

		access002 := accessByID["access-002"]
		Expect(access002.ExternalUserID).NotTo(BeNil())
		Expect(*access002.ExternalUserID).To(Equal(userByName["Bob Jones"].ID))
	})

	It("should have linked config access to the config item via external_config_id", func() {
		var configItem models.ConfigItem
		err := DefaultContext.DB().Where("type = ? AND scraper_id = ?", "Organization", scraperModel.ID).First(&configItem).Error
		Expect(err).NotTo(HaveOccurred())

		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())

		for _, ca := range configAccesses {
			Expect(ca.ConfigID.String()).To(Equal(configItem.ID))
		}
	})

	It("should resolve external_user_id from cache on second scrape", func() {
		db.ExternalUserCache.Flush()

		var initialAccesses []dutymodels.ConfigAccess
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&initialAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		initialCount := len(initialAccesses)

		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(len(configAccesses)).To(Equal(initialCount))

		aliceID, found := db.ExternalUserCache.Get("alice-smith")
		Expect(found).To(BeTrue())
		Expect(aliceID).NotTo(Equal(uuid.Nil))

		bobID, found := db.ExternalUserCache.Get("bob-jones")
		Expect(found).To(BeTrue())
		Expect(bobID).NotTo(Equal(uuid.Nil))
	})
})

var _ = Describe("Config access with external_role_aliases test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-config-access-roles")

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
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users, roles, groups, and config access", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external roles to the database", func() {
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(2))

		roleNames := lo.Map(roles, func(r dutymodels.ExternalRole, _ int) string { return r.Name })
		Expect(roleNames).To(ContainElements("Editor", "Viewer"))
	})

	It("should have saved config access with resolved external_role_id from aliases", func() {
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())

		roleByName := make(map[string]dutymodels.ExternalRole)
		for _, r := range roles {
			roleByName[r.Name] = r
		}

		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(2))

		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		access001 := accessByID["role-access-001"]
		Expect(access001.ExternalRoleID).NotTo(BeNil())
		Expect(*access001.ExternalRoleID).To(Equal(roleByName["Editor"].ID))

		access002 := accessByID["role-access-002"]
		Expect(access002.ExternalRoleID).NotTo(BeNil())
		Expect(*access002.ExternalRoleID).To(Equal(roleByName["Viewer"].ID))
	})

	It("should resolve external_role_id from cache on second scrape", func() {
		db.ExternalRoleCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		editorID, found := db.ExternalRoleCache.Get("editor-role")
		Expect(found).To(BeTrue())
		Expect(editorID).NotTo(Equal(uuid.Nil))

		viewerID, found := db.ExternalRoleCache.Get("viewer-role")
		Expect(found).To(BeTrue())
		Expect(viewerID).NotTo(Equal(uuid.Nil))
	})

	It("should have saved external groups to the database with aliases", func() {
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))

		groupNames := lo.Map(groups, func(g dutymodels.ExternalGroup, _ int) string { return g.Name })
		Expect(groupNames).To(ContainElements("Editors Group", "Viewers Group"))
	})

	It("should have saved config access with resolved external_group_id from aliases", func() {
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())

		groupByName := make(map[string]dutymodels.ExternalGroup)
		for _, g := range groups {
			groupByName[g.Name] = g
		}

		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(2))

		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		access001 := accessByID["role-access-001"]
		Expect(access001.ExternalGroupID).NotTo(BeNil())
		Expect(*access001.ExternalGroupID).To(Equal(groupByName["Editors Group"].ID))

		access002 := accessByID["role-access-002"]
		Expect(access002.ExternalGroupID).NotTo(BeNil())
		Expect(*access002.ExternalGroupID).To(Equal(groupByName["Viewers Group"].ID))
	})

	It("should resolve external_group_id from cache on second scrape", func() {
		db.ExternalGroupCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		editorsID, found := db.ExternalGroupCache.Get("editors-group")
		Expect(found).To(BeTrue())
		Expect(editorsID).NotTo(Equal(uuid.Nil))

		viewersID, found := db.ExternalGroupCache.Get("viewers-group")
		Expect(found).To(BeTrue())
		Expect(viewersID).NotTo(Equal(uuid.Nil))
	})
})

var _ = Describe("Config access with combined user, role, and group aliases test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-config-access-roles")

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
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users, roles, groups, and config access", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved config access with user, role, and group all resolved", func() {
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(1))
		user := users[0]
		Expect(user.Name).To(Equal("Charlie Brown"))

		var roles []dutymodels.ExternalRole
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(2))

		roleByName := make(map[string]dutymodels.ExternalRole)
		for _, r := range roles {
			roleByName[r.Name] = r
		}

		var groups []dutymodels.ExternalGroup
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))

		groupByName := make(map[string]dutymodels.ExternalGroup)
		for _, g := range groups {
			groupByName[g.Name] = g
		}

		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(2))

		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		access001 := accessByID["role-access-001"]
		Expect(access001.ExternalUserID).NotTo(BeNil(), "external_user_id should be resolved")
		Expect(*access001.ExternalUserID).To(Equal(user.ID))
		Expect(access001.ExternalRoleID).NotTo(BeNil(), "external_role_id should be resolved")
		Expect(*access001.ExternalRoleID).To(Equal(roleByName["Editor"].ID))
		Expect(access001.ExternalGroupID).NotTo(BeNil(), "external_group_id should be resolved")
		Expect(*access001.ExternalGroupID).To(Equal(groupByName["Editors Group"].ID))

		access002 := accessByID["role-access-002"]
		Expect(access002.ExternalUserID).NotTo(BeNil(), "external_user_id should be resolved")
		Expect(*access002.ExternalUserID).To(Equal(user.ID))
		Expect(access002.ExternalRoleID).NotTo(BeNil(), "external_role_id should be resolved")
		Expect(*access002.ExternalRoleID).To(Equal(roleByName["Viewer"].ID))
		Expect(access002.ExternalGroupID).NotTo(BeNil(), "external_group_id should be resolved")
		Expect(*access002.ExternalGroupID).To(Equal(groupByName["Viewers Group"].ID))
	})

	It("should correctly resolve all entity types from cache on second scrape", func() {
		db.ExternalUserCache.Flush()
		db.ExternalRoleCache.Flush()
		db.ExternalGroupCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		userID, found := db.ExternalUserCache.Get("charlie-brown")
		Expect(found).To(BeTrue())
		Expect(userID).NotTo(Equal(uuid.Nil))

		roleID, found := db.ExternalRoleCache.Get("editor-role")
		Expect(found).To(BeTrue())
		Expect(roleID).NotTo(Equal(uuid.Nil))

		groupID, found := db.ExternalGroupCache.Get("editors-group")
		Expect(found).To(BeTrue())
		Expect(groupID).NotTo(Equal(uuid.Nil))

		Expect(userID).NotTo(Equal(roleID))
		Expect(userID).NotTo(Equal(groupID))
		Expect(roleID).NotTo(Equal(groupID))
	})
})

var _ = Describe("Config access logs upsert", Ordered, func() {
	fetchAccessLogRefs := func() (dutymodels.ConfigItem, uuid.UUID, api.ScrapeContext, uuid.UUID, func()) {
		var configItem dutymodels.ConfigItem
		err := DefaultContext.DB().Where("scraper_id IS NOT NULL").First(&configItem).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find config item with scraper_id")

		scraperIDValue := lo.FromPtr(configItem.ScraperID)
		Expect(scraperIDValue).NotTo(BeEmpty())

		scraperID, err := uuid.Parse(scraperIDValue)
		Expect(err).NotTo(HaveOccurred(), "failed to parse scraper id")

		err = DefaultContext.DB().First(&dutymodels.ConfigScraper{}, "id = ?", scraperID).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find scraper")

		cleanupExternalUser := func() {}
		var externalUser dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperID).First(&externalUser).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				externalUser = dutymodels.ExternalUser{
					ID:        uuid.New(),
					Name:      "access-log-user",
					AccountID: "test-account",
					UserType:  "user",
					ScraperID: scraperID,
					CreatedAt: time.Now(),
				}
				err = DefaultContext.DB().Create(&externalUser).Error
				Expect(err).NotTo(HaveOccurred(), "failed to create external user")
				cleanupExternalUser = func() {
					err := DefaultContext.DB().Delete(&externalUser).Error
					Expect(err).NotTo(HaveOccurred(), "failed to delete external user")
				}
			} else {
				Expect(err).NotTo(HaveOccurred(), "failed to fetch external user")
			}
		}

		return configItem, scraperID, api.NewScrapeContext(DefaultContext), externalUser.ID, cleanupExternalUser
	}

	cleanupAccessLog := func(configID, externalUserID, scraperID uuid.UUID) {
		err := DefaultContext.DB().Where("config_id = ? AND external_user_id = ? AND scraper_id = ?",
			configID, externalUserID, scraperID).Delete(&dutymodels.ConfigAccessLog{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete access logs")
	}

	It("should update access log when newer", func() {
		configItem, scraperID, scraperCtx, externalUserID, cleanupExternalUser := fetchAccessLogRefs()
		DeferCleanup(func() {
			cleanupAccessLog(configItem.ID, externalUserID, scraperID)
			cleanupExternalUser()
		})

		olderTime := time.Now().Add(-2 * time.Hour)
		latestAccessTime := time.Now().Add(-1 * time.Hour)

		olderLog := dutymodels.ConfigAccessLog{
			ConfigID:       configItem.ID,
			ExternalUserID: externalUserID,
			ScraperID:      scraperID,
			CreatedAt:      olderTime,
			MFA:            false,
		}

		err := db.SaveConfigAccessLog(scraperCtx, &olderLog)
		Expect(err).NotTo(HaveOccurred())

		newerLog := dutymodels.ConfigAccessLog{
			ConfigID:       configItem.ID,
			ExternalUserID: externalUserID,
			ScraperID:      scraperID,
			CreatedAt:      latestAccessTime,
			MFA:            true,
		}

		err = db.SaveConfigAccessLog(scraperCtx, &newerLog)
		Expect(err).NotTo(HaveOccurred())

		var storedLog dutymodels.ConfigAccessLog
		err = DefaultContext.DB().Where("config_id = ? AND external_user_id = ? AND scraper_id = ?",
			configItem.ID, externalUserID, scraperID).First(&storedLog).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(storedLog.MFA).To(BeTrue())
		Expect(storedLog.CreatedAt).To(BeTemporally("~", latestAccessTime, time.Second))
	})

	It("should ignore older access log", func() {
		configItem, scraperID, scraperCtx, externalUserID, cleanupExternalUser := fetchAccessLogRefs()
		DeferCleanup(func() {
			cleanupAccessLog(configItem.ID, externalUserID, scraperID)
			cleanupExternalUser()
		})

		latestAccessTime := time.Now().Add(-1 * time.Hour)
		latestLog := dutymodels.ConfigAccessLog{
			ConfigID:       configItem.ID,
			ExternalUserID: externalUserID,
			ScraperID:      scraperID,
			CreatedAt:      latestAccessTime,
			MFA:            true,
		}

		err := db.SaveConfigAccessLog(scraperCtx, &latestLog)
		Expect(err).NotTo(HaveOccurred())

		olderTime := time.Now().Add(-3 * time.Hour)
		olderLog := dutymodels.ConfigAccessLog{
			ConfigID:       configItem.ID,
			ExternalUserID: externalUserID,
			ScraperID:      scraperID,
			CreatedAt:      olderTime,
			MFA:            false,
		}

		err = db.SaveConfigAccessLog(scraperCtx, &olderLog)
		Expect(err).NotTo(HaveOccurred())

		var storedLog dutymodels.ConfigAccessLog
		err = DefaultContext.DB().Where("config_id = ? AND external_user_id = ? AND scraper_id = ?",
			configItem.ID, externalUserID, scraperID).First(&storedLog).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(storedLog.MFA).To(BeTrue())
		Expect(storedLog.CreatedAt).To(BeTemporally("~", latestAccessTime, time.Second))
	})
})

var _ = Describe("Kubernetes RBAC config access e2e test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-k8s-rbac-access")

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
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save RBAC entities and config access", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved the external role to the database", func() {
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(1))
		Expect(roles[0].Name).To(Equal("pod-reader"))
		Expect(roles[0].RoleType).To(Equal("ClusterRole"))
		Expect(roles[0].AccountID).To(Equal("test-cluster"))
	})

	It("should have saved external users to the database", func() {
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2))

		userNames := lo.Map(users, func(u dutymodels.ExternalUser, _ int) string { return u.Name })
		Expect(userNames).To(ContainElements("my-sa", "admin@example.com"))
	})

	It("should have saved the external group to the database", func() {
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(1))
		Expect(groups[0].Name).To(Equal("system:authenticated"))
		Expect(groups[0].GroupType).To(Equal("Group"))
	})

	It("should have saved config access entries linking subjects to the config item via the role", func() {
		var role dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).First(&role).Error
		Expect(err).NotTo(HaveOccurred())

		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(3))

		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		for _, id := range []string{"rbac-access-sa", "rbac-access-user", "rbac-access-group"} {
			ca := accessByID[id]
			Expect(ca.ExternalRoleID).NotTo(BeNil(), "external_role_id should be resolved for %s", id)
			Expect(*ca.ExternalRoleID).To(Equal(role.ID), "external_role_id should match pod-reader role for %s", id)
		}

		var saUser dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ? AND name = ?", scraperModel.ID, "my-sa").First(&saUser).Error
		Expect(err).NotTo(HaveOccurred())
		saAccess := accessByID["rbac-access-sa"]
		Expect(saAccess.ExternalUserID).NotTo(BeNil())
		Expect(*saAccess.ExternalUserID).To(Equal(saUser.ID))

		var adminUser dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ? AND name = ?", scraperModel.ID, "admin@example.com").First(&adminUser).Error
		Expect(err).NotTo(HaveOccurred())
		userAccess := accessByID["rbac-access-user"]
		Expect(userAccess.ExternalUserID).NotTo(BeNil())
		Expect(*userAccess.ExternalUserID).To(Equal(adminUser.ID))

		var group dutymodels.ExternalGroup
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).First(&group).Error
		Expect(err).NotTo(HaveOccurred())
		groupAccess := accessByID["rbac-access-group"]
		Expect(groupAccess.ExternalGroupID).NotTo(BeNil())
		Expect(*groupAccess.ExternalGroupID).To(Equal(group.ID))
	})

	It("should have config access entries pointing to the correct config item", func() {
		var configItem models.ConfigItem
		err := DefaultContext.DB().Where("scraper_id = ? AND type = ?", scraperModel.ID, "Kubernetes::Pod").First(&configItem).Error
		Expect(err).NotTo(HaveOccurred())

		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())

		configItemID, err := uuid.Parse(configItem.ID)
		Expect(err).NotTo(HaveOccurred())

		for _, ca := range configAccesses {
			Expect(ca.ConfigID).To(Equal(configItemID), "config_id should point to the Pod config item for access %s", ca.ID)
		}
	})
})
