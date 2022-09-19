package db

import (
	"context"
	"database/sql"
	"embed"
	"os"
	"time"

	"github.com/flanksource/commons/logger"
	repoimpl "github.com/flanksource/config-db/db/repository"
	"github.com/jackc/pgx/v4/log/logrusadapter"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/jackc/pgx/v4/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// connection variables
var (
	ConnectionString string
	Schema           = "public"
	LogLevel         = "info"
	HTTPEndpoint     = "http://localhost:8080/db"
	defaultDB        *gorm.DB
)

// Flags ...
func Flags(flags *pflag.FlagSet) {
	flags.StringVar(&ConnectionString, "db", "DB_URL", "Connection string for the postgres database")
	flags.StringVar(&Schema, "db-schema", "public", "")
	flags.StringVar(&LogLevel, "db-log-level", "warn", "")
}

//go:embed migrations/*.sql
var embedMigrations embed.FS

//go:embed all:migrations/_always/*.sql
var embedScripts embed.FS

// Pool ...
var Pool *pgxpool.Pool
var repository repoimpl.Database
var pgxConnectionString string

// MustInit initializes the database or fatally exits
func MustInit() {
	if err := Init(ConnectionString); err != nil {
		logger.Fatalf("Failed to initialize db: %v", err.Error())
	}
}

// Init ...
func Init(connection string) error {
	config, err := pgxpool.ParseConfig(ConnectionString)
	if err != nil {
		return err
	}

	if logger.IsTraceEnabled() {
		logrusLogger := &logrus.Logger{
			Out:          os.Stderr,
			Formatter:    new(logrus.TextFormatter),
			Hooks:        make(logrus.LevelHooks),
			Level:        logrus.DebugLevel,
			ExitFunc:     os.Exit,
			ReportCaller: false,
		}
		config.ConnConfig.Logger = logrusadapter.NewLogger(logrusLogger)
	}
	Pool, err = pgxpool.ConnectConfig(context.Background(), config)
	if err != nil {
		return err
	}

	row := Pool.QueryRow(context.TODO(), "SELECT pg_size_pretty(pg_database_size($1));", config.ConnConfig.Database)
	var size string
	if err := row.Scan(&size); err != nil {
		return err
	}
	logger.Infof("Initialized DB: %s (%s)", config.ConnString(), size)

	pgxConnectionString = stdlib.RegisterConnConfig(config.ConnConfig)

	if err := Migrate(); err != nil {
		return err
	}

	db, err := GetDB()
	if err != nil {
		return err
	}

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: db,
	}), &gorm.Config{
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})

	if err != nil {
		return err
	}

	defaultDB = gormDB
	repository = repoimpl.NewRepo(defaultDB)

	return nil
}

// Migrate ...
func Migrate() error {
	goose.SetTableName("config_db_version")
	goose.SetBaseFS(embedMigrations)
	db, err := GetDB()
	if err != nil {
		return err
	}
	defer db.Close()

	for {
		err = goose.UpByOne(db, "migrations", goose.WithAllowMissing())
		if err == goose.ErrNoNextVersion {
			break
		}
		if err != nil {
			return err
		}
	}

	scripts, _ := embedScripts.ReadDir("migrations/_always")

	for _, file := range scripts {
		script, err := embedScripts.ReadFile("migrations/_always/" + file.Name())
		if err != nil {
			return err
		}
		if _, err := Pool.Exec(context.TODO(), string(script)); err != nil {
			return err
		}
	}
	return nil
}

// GetDB ...
func GetDB() (*sql.DB, error) {
	return sql.Open("pgx", pgxConnectionString)
}

// Ping pings the database for health check
func Ping() error {
	d, _ := defaultDB.DB() // returns *sql.DB
	return d.Ping()
}

// DefaultDB returns the default database connection instance
func DefaultDB() *gorm.DB {
	return defaultDB
}
