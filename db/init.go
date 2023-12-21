package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path"
	"strings"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/pflag"
	"gorm.io/gorm"
)

// connection variables
var (
	ConnectionString string
	Schema           = "public"
	LogLevel         = "info"
	HTTPEndpoint     = "http://localhost:8080/db"
	db               *gorm.DB
	runMigrations    = false

	EmbeddedPGServer *embeddedpostgres.EmbeddedPostgres
	EmbeddedPGPort   = uint32(6432)
	EmbeddedPGDB     = "catalog"
)

// Flags ...
func Flags(flags *pflag.FlagSet) {
	flags.StringVar(&ConnectionString, "db", "DB_URL", "Connection string for the postgres database. Use embedded://<path to dir> to use the embedded database")
	flags.StringVar(&Schema, "db-schema", "public", "")
	flags.StringVar(&LogLevel, "db-log-level", "warn", "")
	flags.BoolVar(&runMigrations, "db-migrations", false, "Run database migrations")
}

// Pool ...
var Pool *pgxpool.Pool

// MustInit initializes the database or fatally exits
func MustInit(ctx context.Context) {
	if err := Init(ctx, ConnectionString); err != nil {
		logger.Fatalf("Failed to initialize db: %v", err.Error())
	}
}

func embeddedDB(database string, port uint32) (string, error) {
	embeddedPath := strings.TrimSuffix(strings.TrimPrefix(ConnectionString, "embedded://"), "/")
	if err := os.Chmod(embeddedPath, 0750); err != nil {
		logger.Errorf("failed to chmod %s: %v", embeddedPath, err)
	}

	logger.Infof("Starting embedded postgres server at %s", embeddedPath)

	EmbeddedPGServer = embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(port).
		DataPath(path.Join(embeddedPath, "data")).
		RuntimePath(path.Join(embeddedPath, "runtime")).
		BinariesPath(path.Join(embeddedPath, "bin")).
		Version(embeddedpostgres.V14).
		Username("postgres").Password("postgres").
		Database(database))

	if err := EmbeddedPGServer.Start(); err != nil {
		return "", fmt.Errorf("error starting embedded postgres: %w", err)
	}

	return fmt.Sprintf("postgres://postgres:postgres@localhost:%d/%s?sslmode=disable", port, database), nil
}

// Init ...
func Init(ctx context.Context, connection string) error {
	var err error

	if strings.HasPrefix(ConnectionString, "embedded://") {
		if connection, err = embeddedDB(EmbeddedPGDB, EmbeddedPGPort); err != nil {
			return fmt.Errorf("failed to setup embedded postgres: %w", err)
		}
	}

	Pool, err = duty.NewPgxPool(connection)
	if err != nil {
		return err
	}

	conn, err := Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if err := conn.Ping(ctx); err != nil {
		return err
	}

	db, err = duty.NewGorm(connection, duty.DefaultGormConfig())
	if err != nil {
		return err
	}

	if runMigrations {
		opts := &migrate.MigrateOptions{
			IgnoreFiles: []string{"012_changelog_triggers_checks.sql", "012_changelog_triggers_others.sql"},
		}
		if err = duty.Migrate(connection, opts); err != nil {
			return err
		}
	}

	// initialize cache
	initCache()
	return nil
}

// GetDB ...
func GetDB(connection string) (*sql.DB, error) {
	return duty.NewDB(connection)
}

// Ping pings the database for health check
func Ping() error {
	d, _ := db.DB() // returns *sql.DB
	return d.Ping()
}

// DefaultDB returns the default database connection instance
func DefaultDB() *gorm.DB {
	return db
}

func StopEmbeddedPGServer() error {
	if EmbeddedPGServer == nil {
		return nil
	}

	logger.Infof("Stopping embedded postgres database server")
	err := EmbeddedPGServer.Stop()
	if err != nil {
		return err
	}

	EmbeddedPGServer = nil
	logger.Infof("Stoped database server")
	return nil
}
