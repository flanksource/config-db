package db

import (
	"time"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("CaptureScrapeSnapshot", Ordered, func() {
	var (
		ctx          api.ScrapeContext
		runStart     time.Time
		scraperAID   uuid.UUID
		scraperBID   uuid.UUID
		scraperAName = "snapshot-test-scraper-A"
		scraperBName = "snapshot-test-scraper-B"

		// Timestamps for seeding. All relative to runStart.
		inRunWindow time.Time // >= runStart               → Last bucket
		lastHour    time.Time // in (-1h, runStart)        → Hour bucket but not Last
		yesterday   time.Time // in (-24h, -1h)            → Day bucket but not Hour
		lastWeek    time.Time // in (-7d, -24h)            → Week bucket but not Day
		longAgo     time.Time // older than 7d             → no windows

		createdConfigIDs   []string
		createdUserIDs     []uuid.UUID
		createdGroupIDs    []uuid.UUID
		createdRoleIDs     []uuid.UUID
		createdAccessIDs   []string
		createdUserGroupKs []struct {
			U, G uuid.UUID
		}
	)

	BeforeAll(func() {
		ctx = api.NewScrapeContext(DefaultContext)
		runStart = time.Now().UTC().Truncate(time.Second)
		inRunWindow = runStart.Add(1 * time.Second)
		lastHour = runStart.Add(-30 * time.Minute)
		yesterday = runStart.Add(-2 * time.Hour)
		lastWeek = runStart.Add(-3 * 24 * time.Hour)
		longAgo = runStart.Add(-30 * 24 * time.Hour)

		// Two scrapers so the per-scraper grouping has multiple keys.
		scraperA := dutymodels.ConfigScraper{
			ID:     uuid.New(),
			Name:   scraperAName,
			Spec:   "{}",
			Source: dutymodels.SourceUI,
		}
		scraperB := dutymodels.ConfigScraper{
			ID:     uuid.New(),
			Name:   scraperBName,
			Spec:   "{}",
			Source: dutymodels.SourceUI,
		}
		Expect(ctx.DB().Create(&scraperA).Error).ToNot(HaveOccurred())
		Expect(ctx.DB().Create(&scraperB).Error).ToNot(HaveOccurred())
		scraperAID = scraperA.ID
		scraperBID = scraperB.ID

		// Seed config items. The config_items table has DB-level defaults of
		// now() on both created_at and updated_at, so an insert with no
		// explicit updated_at still lands as "touched right now". We
		// therefore ALWAYS pin updated_at to an explicit value after insert,
		// using NULL to represent "never touched".
		seedConfig := func(scraperID uuid.UUID, typ string, updatedAt, deletedAt *time.Time) string {
			id := uuid.NewString()
			ci := models.ConfigItem{
				ID:          id,
				ScraperID:   lo.ToPtr(scraperID),
				ConfigClass: "test",
				Type:        typ,
				Name:        lo.ToPtr("snapshot-test-" + id[:8]),
			}
			Expect(ctx.DB().Create(&ci).Error).ToNot(HaveOccurred())
			// Always overwrite updated_at — nil becomes SQL NULL.
			Expect(ctx.DB().Exec("UPDATE config_items SET updated_at = ? WHERE id = ?", updatedAt, id).Error).
				ToNot(HaveOccurred())
			if deletedAt != nil {
				Expect(ctx.DB().Exec("UPDATE config_items SET deleted_at = ? WHERE id = ?", *deletedAt, id).Error).
					ToNot(HaveOccurred())
			}
			createdConfigIDs = append(createdConfigIDs, id)
			return id
		}

		// Scraper A, type Kubernetes::Pod:
		//   1 active, updated in run window
		//   1 active, updated last hour
		//   1 active, updated last week (no newer touches)
		//   1 deleted in run window
		//   1 deleted long ago (outside all windows)
		seedConfig(scraperAID, "Kubernetes::Pod", &inRunWindow, nil)
		seedConfig(scraperAID, "Kubernetes::Pod", &lastHour, nil)
		seedConfig(scraperAID, "Kubernetes::Pod", &lastWeek, nil)
		seedConfig(scraperAID, "Kubernetes::Pod", nil, &inRunWindow)
		seedConfig(scraperAID, "Kubernetes::Pod", nil, &longAgo)

		// Scraper B, type AWS::EC2::Instance:
		//   2 active, one updated yesterday, one never touched since create
		seedConfig(scraperBID, "AWS::EC2::Instance", &yesterday, nil)
		seedConfig(scraperBID, "AWS::EC2::Instance", nil, nil)

		// External users: 3 total
		//   1 updated in run window
		//   1 deleted yesterday
		//   1 untouched
		seedUser := func(updatedAt, deletedAt *time.Time) uuid.UUID {
			u := dutymodels.ExternalUser{
				ID:        uuid.New(),
				ScraperID: scraperAID,
				Name:      "snapshot-test-user-" + uuid.NewString()[:8],
				UserType:  "Member",
				UpdatedAt: updatedAt,
				DeletedAt: deletedAt,
			}
			Expect(ctx.DB().Create(&u).Error).ToNot(HaveOccurred())
			createdUserIDs = append(createdUserIDs, u.ID)
			return u.ID
		}
		uInRun := seedUser(&inRunWindow, nil)
		_ = seedUser(nil, &yesterday)
		_ = seedUser(nil, nil)

		// External groups: 2 total, one updated last hour.
		seedGroup := func(updatedAt, deletedAt *time.Time) uuid.UUID {
			g := dutymodels.ExternalGroup{
				ID:        uuid.New(),
				ScraperID: scraperAID,
				Name:      "snapshot-test-group-" + uuid.NewString()[:8],
				UpdatedAt: updatedAt,
				DeletedAt: deletedAt,
			}
			Expect(ctx.DB().Create(&g).Error).ToNot(HaveOccurred())
			createdGroupIDs = append(createdGroupIDs, g.ID)
			return g.ID
		}
		gInRun := seedGroup(&lastHour, nil)
		_ = seedGroup(nil, nil)

		// External roles: 1, untouched. The DB has a check constraint
		// requiring either scraper_id or application_id to be set.
		{
			r := dutymodels.ExternalRole{
				ID:        uuid.New(),
				ScraperID: lo.ToPtr(scraperAID),
				Aliases:   pq.StringArray{"snapshot-test-role"},
				Name:      "snapshot-test-role",
			}
			Expect(ctx.DB().Create(&r).Error).ToNot(HaveOccurred())
			createdRoleIDs = append(createdRoleIDs, r.ID)
		}

		// External user groups: 1, created so total=1, no deletes.
		{
			ug := dutymodels.ExternalUserGroup{
				ExternalUserID:  uInRun,
				ExternalGroupID: gInRun,
			}
			Expect(ctx.DB().Create(&ug).Error).ToNot(HaveOccurred())
			createdUserGroupKs = append(createdUserGroupKs, struct{ U, G uuid.UUID }{uInRun, gInRun})
		}

		// Config access: 1 active, 1 deleted in run window.
		seedAccess := func(configID uuid.UUID, userID uuid.UUID, deletedAt *time.Time) string {
			id := uuid.NewString()
			a := dutymodels.ConfigAccess{
				ID:             id,
				ScraperID:      lo.ToPtr(scraperAID),
				ConfigID:       configID,
				ExternalUserID: lo.ToPtr(userID),
				DeletedAt:      deletedAt,
			}
			Expect(ctx.DB().Create(&a).Error).ToNot(HaveOccurred())
			createdAccessIDs = append(createdAccessIDs, id)
			return id
		}
		// Need a config item UUID for the config_id FK. Reuse one of the
		// seeded config items — but our db/models.ConfigItem.ID is a string,
		// while ConfigAccess.ConfigID is a uuid.UUID. Parse it back.
		cfgUUID := uuid.MustParse(createdConfigIDs[0])
		seedAccess(cfgUUID, uInRun, nil)
		seedAccess(cfgUUID, uInRun, &inRunWindow)

		// Config access logs: 1 row — the primary key is
		// (config_id, external_user_id, scraper_id), so we can only insert
		// one row per (config, user, scraper) combination. No updated_at or
		// deleted_at columns exist on this table, so snapshot should show
		// a positive Total and all window buckets at zero.
		{
			al := dutymodels.ConfigAccessLog{
				ConfigID:       cfgUUID,
				ExternalUserID: uInRun,
				ScraperID:      scraperAID,
				CreatedAt:      runStart,
				Count:          lo.ToPtr(1),
			}
			Expect(ctx.DB().Create(&al).Error).ToNot(HaveOccurred())
		}
	})

	AfterAll(func() {
		for _, id := range createdConfigIDs {
			_ = ctx.DB().Exec("DELETE FROM config_items WHERE id = ?", id).Error
		}
		for _, id := range createdAccessIDs {
			_ = ctx.DB().Exec("DELETE FROM config_access WHERE id = ?", id).Error
		}
		for _, k := range createdUserGroupKs {
			_ = ctx.DB().Exec("DELETE FROM external_user_groups WHERE external_user_id = ? AND external_group_id = ?", k.U, k.G).Error
		}
		_ = ctx.DB().Exec("DELETE FROM config_access_logs WHERE scraper_id = ?", scraperAID).Error
		for _, id := range createdRoleIDs {
			_ = ctx.DB().Exec("DELETE FROM external_roles WHERE id = ?", id).Error
		}
		for _, id := range createdGroupIDs {
			_ = ctx.DB().Exec("DELETE FROM external_groups WHERE id = ?", id).Error
		}
		for _, id := range createdUserIDs {
			_ = ctx.DB().Exec("DELETE FROM external_users WHERE id = ?", id).Error
		}
		_ = ctx.DB().Exec("DELETE FROM config_scrapers WHERE id IN (?, ?)", scraperAID, scraperBID).Error
	})

	It("captures per-scraper config item counts against the run window", func() {
		snap, err := CaptureScrapeSnapshot(ctx, runStart)
		Expect(err).ToNot(HaveOccurred())
		Expect(snap).ToNot(BeNil())

		a := snap.PerScraper[scraperAName]
		// Scraper A: 4 active (1 deleted-in-run is still deleted; 1 deleted-longAgo
		// also excluded). Active = 3 updated + (the one we deleted long ago is
		// excluded from total). Actually: 5 total rows seeded, 2 with deleted_at.
		// Total = rows with deleted_at IS NULL = 3.
		Expect(a.Total).To(Equal(3), "scraper A active total")
		// Updates: inRunWindow, lastHour, lastWeek → Last=1, Hour=2, Day=2, Week=3.
		// (inRunWindow also counts as >= runStart-1h/-1d/-7d.)
		Expect(a.UpdatedLast).To(Equal(1))
		Expect(a.UpdatedHour).To(Equal(2))
		Expect(a.UpdatedDay).To(Equal(2))
		Expect(a.UpdatedWeek).To(Equal(3))
		// Deletes: inRunWindow → Last/Hour/Day/Week all =1. longAgo outside all.
		Expect(a.DeletedLast).To(Equal(1))
		Expect(a.DeletedHour).To(Equal(1))
		Expect(a.DeletedDay).To(Equal(1))
		Expect(a.DeletedWeek).To(Equal(1))

		b := snap.PerScraper[scraperBName]
		Expect(b.Total).To(Equal(2), "scraper B active total")
		// Only one update, yesterday (-2h): Last=0, Hour=0, Day=1, Week=1.
		Expect(b.UpdatedLast).To(Equal(0))
		Expect(b.UpdatedHour).To(Equal(0))
		Expect(b.UpdatedDay).To(Equal(1))
		Expect(b.UpdatedWeek).To(Equal(1))
	})

	It("captures per-config-type counts across all scrapers", func() {
		snap, err := CaptureScrapeSnapshot(ctx, runStart)
		Expect(err).ToNot(HaveOccurred())

		// Per-type is global across all scrapers and may include rows from
		// other concurrent tests. The assertions below check only the
		// presence of the types we seeded with the expected minimum counts.
		pod := snap.PerConfigType["Kubernetes::Pod"]
		Expect(pod.Total).To(BeNumerically(">=", 3))
		Expect(pod.UpdatedLast).To(BeNumerically(">=", 1))
		Expect(pod.DeletedLast).To(BeNumerically(">=", 1))

		ec2 := snap.PerConfigType["AWS::EC2::Instance"]
		Expect(ec2.Total).To(BeNumerically(">=", 2))
		Expect(ec2.UpdatedDay).To(BeNumerically(">=", 1))
	})

	It("captures external entity windows", func() {
		snap, err := CaptureScrapeSnapshot(ctx, runStart)
		Expect(err).ToNot(HaveOccurred())

		// External users: 3 rows, 1 deleted yesterday → active total contribution is 2.
		// Other tests may add rows, so assert with >=.
		Expect(snap.ExternalUsers.Total).To(BeNumerically(">=", 2))
		Expect(snap.ExternalUsers.UpdatedLast).To(BeNumerically(">=", 1))
		Expect(snap.ExternalUsers.DeletedDay).To(BeNumerically(">=", 1))

		// External groups: 2 rows, both active, one updated last hour.
		Expect(snap.ExternalGroups.Total).To(BeNumerically(">=", 2))
		Expect(snap.ExternalGroups.UpdatedHour).To(BeNumerically(">=", 1))
		Expect(snap.ExternalGroups.UpdatedLast).To(BeNumerically(">=", 0))

		// External roles: 1 row, untouched.
		Expect(snap.ExternalRoles.Total).To(BeNumerically(">=", 1))

		// External user groups: 1 row, untouched.
		Expect(snap.ExternalUserGroups.Total).To(BeNumerically(">=", 1))
		// No updated_at column → UpdatedLast must always be zero.
		Expect(snap.ExternalUserGroups.UpdatedLast).To(Equal(0))
		Expect(snap.ExternalUserGroups.UpdatedWeek).To(Equal(0))
	})

	It("captures config_access with zero updated buckets (no updated_at column)", func() {
		snap, err := CaptureScrapeSnapshot(ctx, runStart)
		Expect(err).ToNot(HaveOccurred())

		// 2 rows seeded, one deleted in run window → 1 active.
		Expect(snap.ConfigAccess.Total).To(BeNumerically(">=", 1))
		Expect(snap.ConfigAccess.DeletedLast).To(BeNumerically(">=", 1))
		// No updated_at column → all updated buckets should be exactly zero.
		Expect(snap.ConfigAccess.UpdatedLast).To(Equal(0))
		Expect(snap.ConfigAccess.UpdatedHour).To(Equal(0))
		Expect(snap.ConfigAccess.UpdatedDay).To(Equal(0))
		Expect(snap.ConfigAccess.UpdatedWeek).To(Equal(0))
	})

	It("captures config_access_logs with only a Total count", func() {
		snap, err := CaptureScrapeSnapshot(ctx, runStart)
		Expect(err).ToNot(HaveOccurred())

		// No updated_at or deleted_at columns → only Total should be populated,
		// all window buckets stay zero.
		Expect(snap.ConfigAccessLogs.Total).To(BeNumerically(">=", 1))
		Expect(snap.ConfigAccessLogs.UpdatedLast).To(Equal(0))
		Expect(snap.ConfigAccessLogs.UpdatedWeek).To(Equal(0))
		Expect(snap.ConfigAccessLogs.DeletedLast).To(Equal(0))
		Expect(snap.ConfigAccessLogs.DeletedWeek).To(Equal(0))
	})

	It("populates CapturedAt and RunStartedAt", func() {
		snap, err := CaptureScrapeSnapshot(ctx, runStart)
		Expect(err).ToNot(HaveOccurred())
		Expect(snap.RunStartedAt.Unix()).To(Equal(runStart.Unix()))
		Expect(snap.CapturedAt.Time).To(BeTemporally(">=", runStart))
	})
})
