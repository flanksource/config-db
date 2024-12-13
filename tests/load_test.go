package tests

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/tests/setup"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func TestLoad(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Load")
}

var (
	DefaultContext context.Context
)

type ChangeTimes []struct {
	ChangeType string
	CreatedAt  time.Time
	Name       string
	Details    string
}

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
		kubeconfig := clientcmd.RecommendedHomeFile
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			panic(err)
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			panic(err)
		}
		DefaultContext = DefaultContext.WithKubernetes(clientset, config)

		scrapeConfig := v1.ScrapeConfig{
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

		os.Remove("log.txt")

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
            SELECT cc.change_type, cc.created_at, ci.name FROM config_changes cc
            INNER JOIN config_items ci ON cc.config_id = ci.id
            WHERE ci.name LIKE 'podinfo%'
            `).Scan(&podinfoChanges).Error

		Expect(err).To(BeNil())

		podinfoChangeDiffs := make(map[string]time.Time)
		for _, c := range podinfoChanges {
			logger.Infof("Change is %v", c)
			if c.ChangeType == v1.ChangeTypeDiff {
				podinfoChangeDiffs[c.Name] = c.CreatedAt
			}
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

			if parts[1] == "crash" {
				k6CrashTime[parts[0]] = t
			} else if parts[1] == "scaledown" {
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
            SELECT cc.change_type, cc.created_at, ci.name FROM config_changes cc
            INNER JOIN config_items ci ON cc.config_id = ci.id
            WHERE ci.name LIKE 'nginx%'
            ORDER BY cc.created_at ASC
            `).Scan(&nginxChanges).Error

		Expect(err).To(BeNil())

		nginxChangeDiffs := make(map[string]time.Time)
		nginxCounter := 0
		for _, c := range nginxChanges {
			logger.Infof("Nginx change is %v", c)
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
