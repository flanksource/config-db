package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/config-db/db/models"
	"github.com/patrickmn/go-cache"
)

var (
	cacheStore *cache.Cache
)

func parentIDExternalCacheKey(externalUID models.CIExternalUID) string {
	return fmt.Sprintf("parent_id:%s:%s", externalUID.ExternalType, strings.Join(externalUID.ExternalID, ","))
}

func parentIDCacheKey(id string) string {
	return fmt.Sprintf("parent_id:%s", id)
}

func initCache() {
	cacheStore = cache.New(24*time.Hour, 3*24*time.Hour)
}
