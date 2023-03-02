package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	pgUniqueViolationErrCode = "23505" // https://hackage.haskell.org/package/postgresql-error-codes-1.0.1/docs/PostgreSQL-ErrorCodes.html#t:ErrorCode
)

func IsUniqueConstraintPGErr(err error) bool {
	pgErr := new(pgconn.PgError)
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolationErrCode
}
