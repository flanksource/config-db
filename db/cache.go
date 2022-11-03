package db

import (
	"fmt"
	"time"

	"github.com/patrickmn/go-cache"
)

var (
	cacheStore *cache.Cache
)

func parentIDCacheKey(id string) string {
	return fmt.Sprintf("parent_id:%s", id)
}

func initCache() {
	cacheStore = cache.New(24*time.Hour, 3*24*time.Hour)
}
