package scrapeui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildConfigMetaFromRelationshipsUsesConfigAsParent(t *testing.T) {
	meta := BuildConfigMetaFromRelationships([]UIRelationship{
		{
			ConfigExternalID:  "sg-1",
			RelatedExternalID: "db-1",
			Relation:          "RDSSecurityGroup",
			ConfigName:        "database-sg",
			RelatedName:       "orders-db",
		},
		{
			ConfigExternalID:  "lb-1",
			RelatedExternalID: "i-1",
			Relation:          "LoadBalancerInstance",
			ConfigName:        "public-elb",
			RelatedName:       "web-1",
		},
	})

	require.Contains(t, meta, "db-1")
	require.Contains(t, meta, "i-1")
	assert.Equal(t, []string{"database-sg"}, meta["db-1"].Parents)
	assert.Equal(t, []string{"public-elb"}, meta["i-1"].Parents)
	assert.NotContains(t, meta, "sg-1")
	assert.NotContains(t, meta, "lb-1")
}
