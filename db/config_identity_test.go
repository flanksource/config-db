package db

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfigItemFromResultDualWriteIdentityFields(t *testing.T) {
	ctx := newTestScrapeContext().WithScrapeConfig(&v1.ScrapeConfig{})

	result := v1.ScrapeResult{
		ID:          "AWS/Resource-123",
		Aliases:     []string{"alias-one", "ALIAS-ONE", "", " alias-two ", "ALIAS-TWO"},
		ConfigClass: "AWS::Resource",
		Type:        "AWS::Resource",
		Name:        "resource-123",
		Source:      "unit-test",
		Config:      map[string]any{"id": "resource-123"},
	}

	ci, err := NewConfigItemFromResult(ctx, result)
	require.NoError(t, err)
	require.NotNil(t, ci)
	require.NotNil(t, ci.ExternalIDV2)

	assert.Equal(t, "AWS/Resource-123", *ci.ExternalIDV2)
	assert.Equal(t, []string{"alias-one", "ALIAS-ONE", "alias-two", "ALIAS-TWO"}, []string(ci.Aliases))
	assert.Equal(t, []string{"AWS/Resource-123", "alias-one", "ALIAS-ONE", "alias-two", "ALIAS-TWO"}, []string(ci.ExternalID))
}
