// Keeps config_costs serviceable: refreshes rollups, reconciles unmatched rows, and
// enforces retention. Cost coarsening is intentionally disabled.
package jobs

import (
	"fmt"
	"sort"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/properties"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
)

var configCostJobs = []*job.Job{RefreshConfigCostsRollup, CompactConfigCosts}

var RefreshConfigCostsRollup = &job.Job{
	Name: "RefreshConfigCostsRollup", Schedule: "@every 1h", Singleton: true,
	JobHistory: true, Retention: job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if err := ctx.DB().Exec("SELECT refresh_config_costs_rollup()").Error; err != nil {
			return err
		}
		ctx.History.SuccessCount++
		return nil
	},
}

var CompactConfigCosts = &job.Job{
	Name: "CompactConfigCosts", Schedule: "@every 1h", Singleton: true,
	JobHistory: true, Retention: job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		attached, ambiguous, err := attachUnmatchedCosts(ctx)
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += attached
		// Ambiguous rows are accounting errors, but successful attachments in the same
		// pass still count so the job is recorded as a warning rather than a total failure.
		if attached == 0 && len(ambiguous) > 0 {
			ctx.History.SuccessCount++
		}
		if len(ambiguous) > 0 {
			ctx.History.AddDetails("ambiguous_config_costs", ambiguous)
			for _, item := range ambiguous {
				ctx.History.AddErrorf("config cost %s is ambiguous across configs %v", item.OrphanID, item.ConfigIDs)
			}
		}
		expired, err := expireConfigCosts(ctx)
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += expired
		return nil
	},
}

type ambiguousConfigCost struct {
	OrphanID  uuid.UUID   `json:"orphan_id"`
	ConfigIDs []uuid.UUID `json:"config_ids"`
}

// attachUnmatchedCosts is a single atomic reconciliation pass. Candidate lookup honors
// the identity retained at ingestion. Ambiguous rows stay unmatched and are reported
// without blocking unambiguous rows. When both an orphan and a matched row exist, the
// newest row wins wholesale; amounts are never summed.
func attachUnmatchedCosts(ctx job.JobRuntime) (int, []ambiguousConfigCost, error) {
	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return 0, nil, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	// Serialize reconciliation against cost upserts so candidates and duplicate rows do
	// not change while the deterministic winner is selected and applied.
	if err := tx.Exec(`LOCK TABLE config_costs IN SHARE ROW EXCLUSIVE MODE`).Error; err != nil {
		tx.Rollback()
		return 0, nil, fmt.Errorf("failed to lock config costs for reconciliation: %w", err)
	}

	candidateSQL := `
		SELECT c.id AS orphan_id, ci.id AS config_id
		FROM config_costs c
		JOIN config_items ci
		  ON c.external_id = ANY(ci.external_id) AND ci.deleted_at IS NULL
		 AND (c.external_config_type IS NULL OR ci.type = c.external_config_type)
		 AND (c.external_config_scraper_id IS NULL OR c.external_config_scraper_id = 'all'
		      OR c.external_config_type IN ('AWS::Region', 'AWS::AvailabilityZone', 'GitHub::Organization')
		      OR ci.scraper_id::text = c.external_config_scraper_id)
		 AND (c.external_config_labels IS NULL OR c.external_config_labels = '{}'::jsonb OR ci.labels @> c.external_config_labels)
		WHERE c.config_id IS NULL
		ORDER BY c.id, ci.id`

	type candidate struct {
		OrphanID uuid.UUID `gorm:"column:orphan_id"`
		ConfigID uuid.UUID `gorm:"column:config_id"`
	}
	var raw []candidate
	if err := tx.Raw(candidateSQL).Scan(&raw).Error; err != nil {
		tx.Rollback()
		return 0, nil, fmt.Errorf("failed to inspect unmatched costs: %w", err)
	}

	matches := make(map[uuid.UUID][]uuid.UUID)
	for _, row := range raw {
		matches[row.OrphanID] = append(matches[row.OrphanID], row.ConfigID)
	}
	ambiguous := make([]ambiguousConfigCost, 0)
	for orphanID, ids := range matches {
		if len(ids) > 1 {
			ambiguous = append(ambiguous, ambiguousConfigCost{OrphanID: orphanID, ConfigIDs: ids})
			delete(matches, orphanID)
		}
	}
	sort.Slice(ambiguous, func(i, j int) bool { return ambiguous[i].OrphanID.String() < ambiguous[j].OrphanID.String() })

	type costVersion struct {
		ID        uuid.UUID `gorm:"column:id"`
		UpdatedAt time.Time `gorm:"column:updated_at"`
	}
	changed := 0
	for orphanID, ids := range matches {
		configID := ids[0]
		var orphan costVersion
		if err := tx.Table("config_costs").Select("id, updated_at").Where("id = ?", orphanID).First(&orphan).Error; err != nil {
			tx.Rollback()
			return 0, nil, fmt.Errorf("failed to load unmatched cost %s: %w", orphanID, err)
		}

		var target costVersion
		targetQuery := tx.Raw(`
			SELECT t.id, t.updated_at
			FROM config_costs o
			JOIN config_costs t
			  ON t.config_id = ? AND t.source_key = o.source_key
			 AND t.period_start = o.period_start AND t.period_end = o.period_end
			 AND t.fingerprint = o.fingerprint
			WHERE o.id = ?
			ORDER BY t.updated_at DESC, t.id DESC
			LIMIT 1`, configID, orphanID).Scan(&target)
		if targetQuery.Error != nil {
			tx.Rollback()
			return 0, nil, fmt.Errorf("failed to inspect matched duplicate for %s: %w", orphanID, targetQuery.Error)
		}

		if target.ID == uuid.Nil {
			if err := tx.Model(&models.ConfigCost{}).Where("id = ?", orphanID).
				UpdateColumn("config_id", configID).Error; err != nil {
				tx.Rollback()
				return 0, nil, fmt.Errorf("failed to attach unmatched cost %s: %w", orphanID, err)
			}
			changed++
			continue
		}

		orphanWins := orphan.UpdatedAt.After(target.UpdatedAt) || (orphan.UpdatedAt.Equal(target.UpdatedAt) && orphan.ID.String() > target.ID.String())
		if orphanWins {
			// Delete the conflicting target before attaching the winning orphan so the unique
			// key is never transiently duplicated.
			if err := tx.Delete(&models.ConfigCost{}, "id = ?", target.ID).Error; err != nil {
				tx.Rollback()
				return 0, nil, fmt.Errorf("failed to delete older matched cost %s: %w", target.ID, err)
			}
			if err := tx.Model(&models.ConfigCost{}).Where("id = ?", orphanID).
				UpdateColumn("config_id", configID).Error; err != nil {
				tx.Rollback()
				return 0, nil, fmt.Errorf("failed to attach winning unmatched cost %s: %w", orphanID, err)
			}
		} else if err := tx.Delete(&models.ConfigCost{}, "id = ?", orphanID).Error; err != nil {
			tx.Rollback()
			return 0, nil, fmt.Errorf("failed to delete older unmatched cost %s: %w", orphanID, err)
		}
		changed++
	}

	if err := tx.Commit().Error; err != nil {
		return 0, nil, err
	}
	return changed, ambiguous, nil
}

func expireConfigCosts(ctx job.JobRuntime) (int, error) {
	retention := properties.String("400d", "config.costs.retention")
	parsed, err := duration.ParseDuration(retention)
	if err != nil {
		return 0, fmt.Errorf("invalid config.costs.retention: %w", err)
	}
	result := ctx.DB().Exec(`DELETE FROM config_costs WHERE period_end < now() - make_interval(secs => ?)`, time.Duration(parsed).Seconds())
	if result.Error != nil {
		return 0, fmt.Errorf("failed to expire config costs: %w", result.Error)
	}
	return int(result.RowsAffected), nil
}
