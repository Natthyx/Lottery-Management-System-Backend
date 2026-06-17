// Package pgerr centralises PostgreSQL error inspection so callers never
// need to string-match error messages.
package pgerr

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL error codes we care about.
// Full list: https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeCheckViolation      = "23514"
	codeNotNullViolation    = "23502"
	codeSerializationError  = "40001"
)

// IsUnique returns true if err is a Postgres unique-constraint violation.
func IsUnique(err error) bool { return code(err) == codeUniqueViolation }

// IsForeignKey returns true on FK constraint violation.
func IsForeignKey(err error) bool { return code(err) == codeForeignKeyViolation }

// IsCheck returns true on CHECK constraint violation.
func IsCheck(err error) bool { return code(err) == codeCheckViolation }

// IsNotNull returns true on NOT NULL constraint violation.
func IsNotNull(err error) bool { return code(err) == codeNotNullViolation }

// IsSerialization returns true on a serialisation failure (retryable).
func IsSerialization(err error) bool { return code(err) == codeSerializationError }

func code(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
