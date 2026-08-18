package cmd

import (
	"testing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutycontext "github.com/flanksource/duty/context"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestScrapeAndStoreWithoutDatabase(t *testing.T) {
	scrapeConfig := v1.ScrapeConfig{ObjectMeta: metav1.ObjectMeta{Name: "standalone"}}
	ctx := api.NewScrapeContext(dutycontext.New()).WithScrapeConfig(&scrapeConfig)

	results, summary, snapshots, err := scrapeAndStore(ctx)
	require.NoError(t, err)
	require.Empty(t, results)
	require.Nil(t, summary)
	require.Nil(t, snapshots)
}
