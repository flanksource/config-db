//go:build debug

package main

import (
	"time"

	"github.com/fjl/memsize"
	"github.com/fjl/memsize/memsizeui"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/pkg/api"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/config-db/utils"
	"github.com/labstack/echo/v4"
)

func init() {
	var memsizeHandler memsizeui.Handler
	utils.TrackObject = func(name string, obj any) {
		if obj == nil {
			go func() {
				time.Sleep(5 * time.Minute)
				utils.TrackObject(name, obj)
			}()
		} else {
			memsizeHandler.Add(name, obj)
		}
	}

	utils.MemsizeScan = func(obj any) uintptr {
		sizes := memsize.Scan(obj)
		return sizes.Total
	}

	utils.MemsizeEchoHandler = func(c echo.Context) error {
		memsizeHandler.ServeHTTP(c.Response(), c.Request())
		return nil
	}

	utils.TrackObject("TempCacheStore", &scrapers.TempCacheStore)
	utils.TrackObject("ScraperTempCache", &api.ScraperTempCache)
	utils.TrackObject("IgnoreCache", &kubernetes.IgnoreCache)
	utils.TrackObject("OrphanCache", &db.OrphanCache)
	utils.TrackObject("ChangeCacheByFingerprint", &db.ChangeCacheByFingerprint)
	utils.TrackObject("ParentCache", &db.ParentCache)
	utils.TrackObject("ResourceIDMapPerCluster", &kubernetes.ResourceIDMapPerCluster)
}
