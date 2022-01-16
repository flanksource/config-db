package db

import (
	"context"
	"database/sql"
	"embed"
	"os"

	"github.com/flanksource/commons/logger"
	"github.com/jackc/pgx/v4/log/logrusadapter"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/jackc/pgx/v4/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/volatiletech/sqlboiler/v4/boil"
)

var ConnectionString string
var Schema = "public"
var LogLevel = "info"
var HttpEndpoint = "http://localhost:8080/db"

func Flags(flags *pflag.FlagSet) {
	flags.StringVar(&ConnectionString, "db", "DB_URL", "Connection string for the postgres database")
	flags.StringVar(&Schema, "db-schema", "public", "")
	flags.StringVar(&LogLevel, "db-log-level", "warn", "")
}

//go:embed migrations/*.sql
var embedMigrations embed.FS
var Pool *pgxpool.Pool
var pgxConnectionString string

func readFromEnv(v string) string {
	val := os.Getenv(v)
	if val != "" {
		return val
	}
	return v
}

func Init(connection string) error {
	ConnectionString = readFromEnv(connection)
	Schema = readFromEnv(Schema)
	LogLevel = readFromEnv(LogLevel)

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
	boil.SetDB(db)
	logger.Infof("Initialized DB: %s", boil.GetDB())

	return nil
}

func Migrate() error {
	goose.SetBaseFS(embedMigrations)
	db, err := GetDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.Up(db, "migrations"); err != nil {
		return err
	}
	return nil
}

func GetDB() (*sql.DB, error) {
	return sql.Open("pgx", pgxConnectionString)
}
