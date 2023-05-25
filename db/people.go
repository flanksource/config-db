package db

import (
	"context"
	"errors"

	"github.com/flanksource/duty/models"
	"gorm.io/gorm"
)

func FindPersonByEmail(ctx context.Context, email string) (*models.Person, error) {
	var person models.Person
	err := db.WithContext(ctx).Where("email = ?", email).First(&person).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}

		return nil, err
	}

	return &person, err
}
