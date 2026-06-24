// Package repository provides PostgreSQL-backed storage for the shipping domain.
package repository

import (
	"context"

	"github.com/dengankarya/connector/pkg/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Type aliases for backwards compatibility — the canonical implementations are in pkg/postgres.
type DBTX = postgres.DBTX

var (
	WithTx              = postgres.WithTx
	TxFromContext       = postgres.TxFromContext
	DBFromContext       = postgres.DBFromContext
	IsDuplicateKeyError = postgres.IsDuplicateKeyError
	NilIfEmpty          = postgres.NilIfEmpty
	NilIfEmptyPtr       = postgres.NilIfEmptyPtr
)

// dbFromContext returns the active transaction if one exists in ctx, else the pool.
// Uses the canonical context key from pkg/postgres.
func dbFromContext(ctx context.Context, pool *pgxpool.Pool) DBTX {
	return postgres.DBFromContext(ctx, pool)
}

// isDuplicateKeyError returns true for PostgreSQL unique-constraint violations (SQLSTATE 23505).
func isDuplicateKeyError(err error) bool {
	return postgres.IsDuplicateKeyError(err)
}

// nilIfEmpty returns nil when s is empty so nullable TEXT columns store NULL rather than "".
func nilIfEmpty(s string) *string {
	return postgres.NilIfEmpty(s)
}

// nilIfEmptyPtr returns nil when p is nil or points to an empty string.
func nilIfEmptyPtr(p *string) *string {
	return postgres.NilIfEmptyPtr(p)
}
