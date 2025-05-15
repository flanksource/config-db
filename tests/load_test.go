package tests

import (
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/tests/setup"
	"github.com/google/uuid"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestLoad(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Load")
}

var (
	DefaultContext context.Context
)

type ChangeTime struct {
	ChangeType string
	CreatedAt  time.Time
	Name       string
	Details    string
	ConfigID   string
	ConfigType string
}

type ChangeTimes []ChangeTime

var _ = ginkgo.BeforeSuite(func() {
	DefaultContext = setup.BeforeSuiteFn()

})

var _ = ginkgo.AfterSuite(setup.AfterSuiteFn)

var _ = ginkgo.Describe("Load Test", ginkgo.Ordered, func() {

	var scraperCtx api.ScrapeContext
	ginkgo.BeforeAll(func() {
		// Skip load test for normal flow
		if _, ok := os.LookupEnv("LOAD_TEST"); !ok {
			ginkgo.Skip("Skipping load test, env: LOAD_TEST not set")
		}

		// This is required since duty.Setup uses a Fake Kubernetes Client by default
		DefaultContext = DefaultContext.WithKubernetes(connection.KubernetesConnection{})

		scrapeConfig := v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name: "k8s",
				UID:  types.UID(uuid.New().String()),
			},
			Spec: v1.ScraperSpec{
				Schedule: "@every 30s",
				Kubernetes: []v1.Kubernetes{{
					ClusterName: "load-test",
					Watch: []v1.KubernetesResourceToWatch{
						{ApiVersion: "v1", Kind: "Namespace"},
						{ApiVersion: "v1", Kind: "Pod"},
					},
				}},
			},
		}

		if _, _, err := db.PersistScrapeConfigFromCRD(DefaultContext, &scrapeConfig); err != nil {
			panic(err)
		}
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scrapers.InitSemaphoreWeights(scraperCtx.Context)
	})

	ginkgo.It("should start scrape once", func() {
		_, err := scrapers.RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var count int64
		Expect(scraperCtx.DB().Table("config_items").Where("type LIKE 'Kubernetes::%'").Count(&count).Error).To(BeNil())
		Expect(count).ToNot(Equal(int64(0)))
	})

	ginkgo.It("should start consumer", func() {
		_ = models.ConfigChange{}
		err := scrapers.SyncScrapeJob(scraperCtx)
		Expect(err).To(BeNil())

		os.Remove("log.txt") // nolint:errcheck

		time.Sleep(15 * time.Second)
		logger.Infof("Exec k6")
		cmd := exec.Command("../fixtures/load/k6", "run", "../fixtures/load/load.ts", "--insecure-skip-tls-verify")
		err = cmd.Run()
		if err != nil {
			logger.Errorf("Error is %v", err)
			panic(err)
		}

		logger.Infof("End k6")
		time.Sleep(2 * time.Minute)

		var count int64
		Expect(scraperCtx.DB().Table("config_changes").Count(&count).Error).To(BeNil())
		Expect(count).ToNot(Equal(int64(0)))

		var podinfoChanges ChangeTimes
		err = scraperCtx.DB().Raw(`
            SELECT cc.change_type, cc.created_at, ci.name, ci.type as config_type, cc.config_id FROM config_changes cc
            INNER JOIN config_items ci ON cc.config_id = ci.id
            WHERE ci.name LIKE 'podinfo%'
            `).Scan(&podinfoChanges).Error

		Expect(err).To(BeNil())

		podinfoChangeDiffs := make(map[string]time.Time)
		podinfoChangeByName := make(map[string][]string)
		for _, c := range podinfoChanges {
			logger.Infof("Change is %v", c)
			podinfoChangeByName[c.Name] = append(podinfoChangeByName[c.Name], c.ChangeType)
			if c.ChangeType == v1.ChangeTypeDiff {
				podinfoChangeDiffs[c.Name] = c.CreatedAt
			}
		}

		// podinfo-0 - podinfo-9
		Expect(len(slices.Collect(maps.Keys(podinfoChangeByName)))).To(Equal(10))

		podChangeTypes := []string{"Healthy", "Pulling", "Scheduled", "diff", "Created", "Started", "Pulled"}
		for n, ct := range podinfoChangeByName {
			_, diff := lo.Difference(ct, podChangeTypes)
			if len(diff) != 0 {
				logger.Infof("Got pod change events: %s -> %v", n, ct)
			}
			Expect(len(diff)).To(Equal(0))
		}

		f, err := os.ReadFile("log.txt")
		Expect(err).To(BeNil())
		lines := strings.Split(string(f), "\n")

		k6CrashTime := make(map[string]time.Time)
		deployTimes := make(map[string]time.Time)
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.Split(line, ",")
			t, err := time.Parse(time.RFC3339, parts[2])
			Expect(err).To(BeNil())

			switch parts[1] {
			case "crash":
				k6CrashTime[parts[0]] = t
			case "scaledown":
				deployTimes[parts[0]] = t
			}

			logger.Infof("N=%s t=%s", parts[0], t)
		}

		for k, v := range k6CrashTime {
			changeLog, ok := podinfoChangeDiffs[k]
			if !ok {
				panic("not found " + k)
			}
			td := changeLog.Sub(v)
			logger.Infof("Delta for %s is %v", k, td)
			Expect(td).To(BeNumerically("<", time.Minute))
		}

		var nginxChanges ChangeTimes
		err = scraperCtx.DB().Raw(`
            SELECT cc.change_type, cc.created_at, ci.name, ci.type as config_type, cc.config_id FROM config_changes cc
            INNER JOIN config_items ci ON cc.config_id = ci.id
            WHERE ci.name LIKE 'nginx%'
            ORDER BY cc.created_at ASC
            `).Scan(&nginxChanges).Error

		Expect(err).To(BeNil())

		scalingReplicaSetEventCount := lo.CountBy(nginxChanges, func(c ChangeTime) bool {
			return c.ChangeType == "ScalingReplicaSet"
		})
		Expect(scalingReplicaSetEventCount).To(Equal(2))

		nginxChangeDiffs := make(map[string]time.Time)
		nginxCounter := 0
		for _, c := range nginxChanges {
			logger.Infof("Nginx change is %v", c)
			// There will be 2 events, one on setup and one when we manually
			// scale down
			if c.ChangeType == "ScalingReplicaSet" && nginxCounter != 0 {
				nginxChangeDiffs[c.Name] = c.CreatedAt
			}
			nginxCounter += 1
		}

		for k, v := range deployTimes {
			changeLog, ok := nginxChangeDiffs[k]
			if !ok {
				panic("not found " + k)
			}
			td := changeLog.Sub(v)
			logger.Infof("Delta for %s is %v", k, td)
			Expect(td).To(BeNumerically("<", time.Minute))
		}
	})
})
