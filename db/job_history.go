package db

import (
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
)

func PersistJobHistory(ctx api.ScrapeContext, h *models.JobHistory) error {
	if ctx.DB() == nil {
		return nil
	}

	// Delete jobs which did not process anything
	if h.ID != uuid.Nil && (h.SuccessCount+h.ErrorCount) == 0 {
		return ctx.DB().Table("job_history").Delete(h).Error
	}

	return ctx.DB().Table("job_history").Save(h).Error
}
