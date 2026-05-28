// Package repository provides PostgreSQL-backed storage for the shipping domain.
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is satisfied by both *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ctxKey string

const txCtxKey ctxKey = "shipping_pgx_tx"

// WithTx stores a pgx.Tx in the context for repository methods to pick up.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txCtxKey, tx)
}

// dbFromContext returns the active transaction if one exists in ctx, else the pool.
func dbFromContext(ctx context.Context, pool *pgxpool.Pool) DBTX {
	if tx, _ := ctx.Value(txCtxKey).(pgx.Tx); tx != nil {
		return tx
	}
	return pool
}

// isDuplicateKeyError returns true for PostgreSQL unique-constraint violations (SQLSTATE 23505).
func isDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// nilIfEmpty returns nil when s is empty so nullable TEXT columns store NULL rather than "".
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nilIfEmptyPtr returns nil when p is nil or points to an empty string.
func nilIfEmptyPtr(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}
