package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	pgUniqueViolationErrCode = "23505" // https://hackage.haskell.org/package/postgresql-error-codes-1.0.1/docs/PostgreSQL-ErrorCodes.html#t:ErrorCode
)

// IsUniqueConstraintPGErr identifies whether the error is a postgres unique constraint error.
//
// gorm doesn't have a native way to identify this error
// https://github.com/go-gorm/gorm/issues/4037
func IsUniqueConstraintPGErr(err error) bool {
	pgErr := new(pgconn.PgError)
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolationErrCode
}
