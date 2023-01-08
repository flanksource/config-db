package db

import (
	"github.com/flanksource/config-db/db/models"
)

func SaveJobHistories(histories models.JobHistories) error {
	// For tests
	if db == nil {
		return nil
	}

	if len(histories) == 0 {
		return nil
	}
	return db.Table("job_history").Create(histories.Prepare()).Error
}
