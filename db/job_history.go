package db

import (
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
)

func PersistJobHistory(h *models.JobHistory) error {
	if db == nil {
		return nil
	}

	// Delete jobs which did not process anything
	if h.ID != uuid.Nil && (h.SuccessCount+h.ErrorCount) == 0 {
		return db.Table("job_history").Delete(h).Error
	}

	return db.Table("job_history").Save(h).Error
}
