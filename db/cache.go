package db

import (
	"fmt"
)

func parentIDExternalCacheKey(externalType, externalID string) string {
	return fmt.Sprintf("parent_id:%s:%s", externalType, externalID)
}

func parentIDCacheKey(id string) string {
	return fmt.Sprintf("parent_id:%s", id)
}
