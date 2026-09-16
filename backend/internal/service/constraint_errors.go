package service

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation and isForeignKeyViolation translate the two Postgres
// constraint errors this codebase can actually provoke through normal use
// into something a caller can act on.
//
// This matters because most handlers answer a service error with
// writeError(w, 400, err.Error()) -- fine for a real validation message
// ("name is required"), but it means an untranslated driver error reaches
// the API response verbatim. A duplicate admin email used to come back as
//
//	duplicate key value violates unique constraint "users_tenant_email_uq" (SQLSTATE 23505)
//
// which names internal table and constraint identifiers and tells the admin
// nothing about what to change.

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation reports whether err is a Postgres foreign-key
// violation (SQLSTATE 23503) -- in practice, a request naming an id that
// doesn't exist (a roleId, say), which is a 400-with-a-clear-message
// situation rather than something to surface raw.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
