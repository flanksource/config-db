package cmd

import (
	"encoding/json"
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/tests/setup"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestEnsureScraperUpdatesExistingSpec(t *testing.T) {
	ctx := setup.BeforeSuiteFn(setup.WithoutDummyData)
	defer setup.AfterSuiteFn()

	existingID := uuid.New()
	duplicateID := uuid.New()
	name := "kubernetes-scraper-" + existingID.String()
	oldSpec := `{"kubernetes":[{"clusterName":"demo","kubeconfig":{"value":"testdata/my-kube-config.yaml"}}]}`
	require.NoError(t, ctx.DB().Create(&models.ConfigScraper{
		ID:        existingID,
		Name:      name,
		Namespace: "default",
		Source:    models.SourceUI,
		Spec:      oldSpec,
	}).Error)
	require.NoError(t, ctx.DB().Create(&models.ConfigScraper{
		ID:        duplicateID,
		Name:      name,
		Namespace: "default",
		Source:    models.SourceUI,
		Spec:      oldSpec,
	}).Error)
	defer ctx.DB().Unscoped().Delete(&models.ConfigScraper{}, "id = ?", existingID)
	defer ctx.DB().Unscoped().Delete(&models.ConfigScraper{}, "id = ?", duplicateID)

	sc := v1.ScrapeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "mc",
		},
		Spec: v1.ScraperSpec{
			Kubernetes: []v1.Kubernetes{{
				ClusterName: "local-kind-cluster",
			}},
		},
	}

	require.NoError(t, ensureScraper(ctx, &sc))

	assert.Equal(t, types.UID(existingID.String()), sc.UID)

	var got models.ConfigScraper
	require.NoError(t, ctx.DB().First(&got, "id = ?", existingID).Error)
	assert.Equal(t, "mc", got.Namespace)
	assert.Equal(t, models.SourceConfigFile, got.Source)
	assert.NotContains(t, got.Spec, "kubeconfig")
	assert.Contains(t, got.Spec, "local-kind-cluster")

	var spec map[string]any
	require.NoError(t, json.Unmarshal([]byte(got.Spec), &spec))
	require.Contains(t, spec, "kubernetes")

	var duplicate models.ConfigScraper
	require.NoError(t, ctx.DB().First(&duplicate, "id = ?", duplicateID).Error)
	assert.Equal(t, "mc", duplicate.Namespace)
	assert.Equal(t, models.SourceConfigFile, duplicate.Source)
	assert.NotContains(t, duplicate.Spec, "kubeconfig")
}
