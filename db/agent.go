package db

import (
	"context"
	"errors"

	"github.com/flanksource/duty/models"
	"gorm.io/gorm"
)

func FindAgentByName(ctx context.Context, name string) (*models.Agent, error) {
	var agent models.Agent
	err := db.WithContext(ctx).First(&agent, "name = ?", name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &agent, nil
}
