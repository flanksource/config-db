package scraper

import (
	"context"
	"fmt"
	"strings"

	"github.com/flanksource/duty"
	"github.com/flanksource/duty/models"
	"gorm.io/gorm"
)

func FindConnectionFromConnectionString(ctx context.Context, db *gorm.DB, connectionString string) (*models.Connection, error) {
	name, connectionType, found := extractConnectionNameType(connectionString)
	if !found {
		return nil, nil
	}

	connection, err := duty.FindConnection(ctx, db, connectionType, name)
	if err != nil {
		return nil, fmt.Errorf("failed to find connection (type=%s, name=%s): %w", connectionType, name, err)
	}

	return connection, nil
}

func extractConnectionNameType(connectionString string) (name string, connectionType string, found bool) {
	prefix := "connection://"

	if !strings.HasPrefix(connectionString, prefix) {
		return
	}

	connectionString = strings.TrimPrefix(connectionString, prefix)
	parts := strings.SplitN(connectionString, "/", 2)
	if len(parts) != 2 {
		return
	}

	if parts[0] == "" || parts[1] == "" {
		return
	}

	return parts[1], parts[0], true
}
