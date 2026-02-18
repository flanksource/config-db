package scrapers

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	. "github.com/onsi/ginkgo/v2"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getConfigSpec(name string) v1.ScrapeConfig {
	configs, err := v1.ParseConfigs("fixtures/" + name + ".yaml")
	if err != nil {
		Fail(fmt.Sprintf("Failed to parse config: %v", err))
	}
	return configs[0]
}

func getFixtureResult(fixture string) []v1.ScrapeResult {
	data, err := os.ReadFile("fixtures/expected/" + fixture + ".json")
	if err != nil {
		Fail(fmt.Sprintf("Failed to read fixture: %v", err))
	}
	var results []v1.ScrapeResult

	if err := json.Unmarshal(data, &results); err != nil {
		Fail(fmt.Sprintf("Failed to unmarshal fixture: %v", err))
	}
	return results
}

func createRandomConfigMap(name string) *apiv1.ConfigMap {
	cm1 := &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: map[string]string{
			"key": fmt.Sprintf("value-%d", rand.Int64()),
		},
	}

	err := k8sClient.Create(gocontext.Background(), cm1)
	if err != nil {
		Fail(fmt.Sprintf("failed to create ConfigMap: %v", err))
	}

	return cm1
}

func GetConfigItemFromID(ctx api.ScrapeContext, id string) (*models.ConfigItem, error) {
	var ci models.ConfigItem
	err := ctx.DB().Limit(1).Omit("config").Find(&ci, "id = ?", id).Error
	return &ci, err
}
