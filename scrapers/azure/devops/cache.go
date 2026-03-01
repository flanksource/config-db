package devops

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/config-db/api"
)

// terminalRunCache is a package-level cache of terminal run ExternalChangeIDs,
// populated from config_changes on first use and refreshed after TTL expires.
var terminalRunCache = &runCache{}

type runCache struct {
	sync.RWMutex
	ids        map[string]struct{}
	lastLoaded time.Time
}

// has reports whether externalChangeID is a known terminal run.
func (c *runCache) has(externalChangeID string) bool {
	c.RLock()
	defer c.RUnlock()
	_, ok := c.ids[externalChangeID]
	return ok
}

// add marks externalChangeID as terminal in the in-process cache.
func (c *runCache) add(externalChangeID string) {
	c.Lock()
	defer c.Unlock()
	if c.ids == nil {
		c.ids = make(map[string]struct{})
	}
	c.ids[externalChangeID] = struct{}{}
}

// ensureFresh reloads the cache from DB when TTL has elapsed.
// pattern is a SQL LIKE prefix to filter relevant rows (e.g. "MyProject/%").
func (c *runCache) ensureFresh(ctx api.ScrapeContext, ttl time.Duration, project string, pipelineID int) error {
	c.RLock()
	stale := c.ids == nil || time.Since(c.lastLoaded) >= ttl
	c.RUnlock()

	if !stale {
		return nil
	}

	pattern := fmt.Sprintf("%s/%d/%%", project, pipelineID)
	return c.load(ctx, pattern)
}

func (c *runCache) load(ctx api.ScrapeContext, pattern string) error {
	if ctx.DB() == nil {
		return nil
	}

	changeTypes := make([]string, 0, len(terminalChangeTypes))
	for ct := range terminalChangeTypes {
		changeTypes = append(changeTypes, ct)
	}

	placeholders := make([]string, len(changeTypes))
	args := make([]any, 0, len(changeTypes)+1)
	args = append(args, pattern)
	for i, ct := range changeTypes {
		placeholders[i] = "?"
		args = append(args, ct)
	}

	query := fmt.Sprintf(
		"SELECT external_change_id FROM config_changes WHERE external_change_id LIKE ? AND change_type IN (%s) AND external_change_id IS NOT NULL",
		strings.Join(placeholders, ","),
	)

	var rows []struct {
		ExternalChangeID string `gorm:"column:external_change_id"`
	}
	if err := ctx.DB().Raw(query, args...).Scan(&rows).Error; err != nil {
		return fmt.Errorf("failed to load terminal run cache: %w", err)
	}

	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		ids[row.ExternalChangeID] = struct{}{}
	}

	c.Lock()
	c.ids = ids
	c.lastLoaded = time.Now()
	c.Unlock()

	ctx.Logger.V(4).Infof("terminal-run cache loaded %d entries for pattern %s", len(ids), pattern)
	return nil
}

// pipelineDefCache caches PipelineDefinition by pipeline ID + Revision to avoid
// redundant GetPipelineWithDefinition calls when the pipeline hasn't changed.
var pipelineDefCache = &defCache{}

type cachedPipelineDef struct {
	Revision   int
	Definition *PipelineDefinition
}

type defCache struct {
	sync.RWMutex
	entries map[int]cachedPipelineDef
}

// get returns a cached definition if the revision matches.
func (c *defCache) get(pipelineID, revision int) (*PipelineDefinition, bool) {
	c.RLock()
	defer c.RUnlock()
	entry, ok := c.entries[pipelineID]
	if !ok || entry.Revision != revision {
		return nil, false
	}
	return entry.Definition, true
}

// set stores a definition in the cache.
func (c *defCache) set(pipelineID, revision int, def *PipelineDefinition) {
	c.Lock()
	defer c.Unlock()
	if c.entries == nil {
		c.entries = make(map[int]cachedPipelineDef)
	}
	c.entries[pipelineID] = cachedPipelineDef{Revision: revision, Definition: def}
}
