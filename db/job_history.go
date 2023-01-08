package db

import (
	"github.com/flanksource/config-db/db/models"
)

func SaveJobHistories(histories models.JobHistories) error {
	if len(histories) == 0 {
		return nil
	}
	return db.Table("job_history").Create(histories.Prepare()).Error
}
