package changes

import (
	"sync"

	"github.com/flanksource/duty/context"

	"github.com/flanksource/config-db/db/models"
)

var ExternalChangeIDCache = sync.Map{}

func AddToExteranlChangeIDCache(changes []*models.ConfigChange) {
	for _, c := range changes {
		if c.ExternalChangeID != nil {
			ExternalChangeIDCache.Store(*c.ExternalChangeID, struct{}{})
		}
	}
}

func InitExternalChangeIDCache(ctx context.Context) error {
	var externalIDs []string
	if err := ctx.DB().Select("external_change_id").Model(&models.ConfigChange{}).Where("external_change_id IS NOT NULL").Find(&externalIDs).Error; err != nil {
		return err
	}

	ctx.Logger.Debugf("initializing external change id cache with %d ids", len(externalIDs))

	for _, c := range externalIDs {
		ExternalChangeIDCache.Store(c, struct{}{})
	}

	return nil
}
