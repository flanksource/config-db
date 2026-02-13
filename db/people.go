package db

import (
	"errors"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"gorm.io/gorm"
)

// GetAllHumanPeople returns all people that are linked to a real user
func GetAllHumanPeople(ctx context.Context) ([]models.Person, error) {
	var people []models.Person
	if err := ctx.DB().
		Where("deleted_at IS NULL").
		Where("type IS NULL"). // ignores type=agent,access_token
		Where("email IS NOT NULL").
		Find(&people).
		Error; err != nil {
		return nil, err
	}

	return people, nil
}

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
