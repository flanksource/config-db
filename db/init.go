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
	dutycontext "github.com/flanksource/duty/context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/pflag"
	"gorm.io/gorm"
)

// connection variables
var (
	ConnectionString string
	Schema           = "public"
	PGRSTLogLevel    = "info"
	HTTPEndpoint     = "http://localhost:8080/db"
	runMigrations    = false

	EmbeddedPGServer *embeddedpostgres.EmbeddedPostgres
	EmbeddedPGPort   = uint32(6432)
	EmbeddedPGDB     = "catalog"
)

// Flags ...
func Flags(flags *pflag.FlagSet) {
	flags.StringVar(&ConnectionString, "db", "DB_URL", "Connection string for the postgres database. Use embedded://<path to dir> to use the embedded database")
	flags.StringVar(&Schema, "db-schema", "public", "")
	flags.StringVar(&PGRSTLogLevel, "postgrest-log-level", "warn", "")
	flags.BoolVar(&runMigrations, "db-migrations", false, "Run database migrations")
}

// Pool ...
var Pool *pgxpool.Pool

// MustInit initializes the database or fatally exits
func MustInit() dutycontext.Context {
	if c, err := Init(ConnectionString); err != nil {
		logger.Fatalf("Failed to initialize db: %v", err.Error())
		return dutycontext.New()
	} else {
		return *c
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
func Init(connection string) (*dutycontext.Context, error) {
	var err error

	if strings.HasPrefix(ConnectionString, "embedded://") {
		if connection, err = embeddedDB(EmbeddedPGDB, EmbeddedPGPort); err != nil {
			return nil, fmt.Errorf("failed to setup embedded postgres: %w", err)
		}

		// Update globally for postgrest
		ConnectionString = connection
	}

	Pool, err = duty.NewPgxPool(connection)
	if err != nil {
		return nil, err
	}

	conn, err := Pool.Acquire(context.TODO())
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if err := conn.Ping(context.TODO()); err != nil {
		return nil, err
	}

	var db *gorm.DB
	db, err = duty.NewGorm(connection, duty.DefaultGormConfig())
	if err != nil {
		return nil, err
	}

	if runMigrations {
		if err = duty.Migrate(connection, nil); err != nil {
			return nil, err
		}
	}

	dutyCtx := dutycontext.New().WithDB(db, Pool)
	return &dutyCtx, nil
}

// GetDB ...
func GetDB(connection string) (*sql.DB, error) {
	return duty.NewDB(connection)
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
