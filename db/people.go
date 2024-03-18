package db

import (
	"errors"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/models"
	"gorm.io/gorm"
)

func FindPersonByEmail(ctx api.ScrapeContext, email string) (*models.Person, error) {
	var person models.Person
	err := ctx.DB().Where("email = ?", email).First(&person).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}

		return nil, err
	}

	return &person, err
}
