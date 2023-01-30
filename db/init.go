package db

import (
	"context"
	"database/sql"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty"
	"github.com/jackc/pgx/v4/pgxpool"
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
)

// Flags ...
func Flags(flags *pflag.FlagSet) {
	flags.StringVar(&ConnectionString, "db", "DB_URL", "Connection string for the postgres database")
	flags.StringVar(&Schema, "db-schema", "public", "")
	flags.StringVar(&LogLevel, "db-log-level", "warn", "")
}

// Pool ...
var Pool *pgxpool.Pool
var pgxConnectionString string

// MustInit initializes the database or fatally exits
func MustInit() {
	if err := Init(ConnectionString); err != nil {
		logger.Fatalf("Failed to initialize db: %v", err.Error())
	}
}

// Init ...
func Init(connection string) error {
	var err error
	Pool, err := duty.NewPgxPool(connection)
	if err != nil {
		return err
	}

	conn, err := Pool.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer conn.Release()

	if err := conn.Ping(context.Background()); err != nil {
		return err
	}

	db, err = duty.NewGorm(connection, duty.DefaultGormConfig())
	if err != nil {
		return err
	}

	if err = duty.Migrate(connection); err != nil {
		return err
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
