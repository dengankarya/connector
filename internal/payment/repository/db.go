// Package repository provides PostgreSQL-backed implementations of the payment domain repositories.
package repository

import (
	"context"

	"github.com/dengankarya/connector/pkg/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Type aliases for backwards compatibility — the canonical implementations are in pkg/postgres.
type DBTX = postgres.DBTX
type TxRunner = postgres.TxRunner

var (
	NewTxRunner         = postgres.NewTxRunner
	WithTx              = postgres.WithTx
	TxFromContext       = postgres.TxFromContext
	DBFromContext       = postgres.DBFromContext
	IsDuplicateKeyError = postgres.IsDuplicateKeyError
	NilIfEmpty          = postgres.NilIfEmpty
	NilIfEmptyPtr       = postgres.NilIfEmptyPtr
)

// dbFromContext is the private version used by repositories in this package.
// Public code should use DBFromContext from postgres package.
func dbFromContext(ctx context.Context, pool *pgxpool.Pool) DBTX {
	return postgres.DBFromContext(ctx, pool)
}

// isDuplicateKeyError is the private version used by repositories in this package.
// Public code should use IsDuplicateKeyError from postgres package.
func isDuplicateKeyError(err error) bool {
	return postgres.IsDuplicateKeyError(err)
}
