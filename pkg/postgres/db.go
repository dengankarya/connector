// Package postgres provides shared PostgreSQL infrastructure for all modules.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ctxKey is an unexported key type to prevent collisions in context values.
type ctxKey string

// txCtxKey is the canonical context key for storing pgx.Tx across modules.
const txCtxKey ctxKey = "pgx_tx"

// DBTX is satisfied by both *pgxpool.Pool and pgx.Tx.
// All repository methods accept DBTX so they work inside or outside a transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// WithTx stores a pgx.Tx in the context so repositories can pick it up automatically.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txCtxKey, tx)
}

// TxFromContext retrieves a pgx.Tx from the context, or nil if none was stored.
func TxFromContext(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(txCtxKey).(pgx.Tx)
	return tx
}

// DBFromContext returns the active transaction if one is in context, else the pool.
// This allows a single repository instance to transparently participate in transactions.
func DBFromContext(ctx context.Context, pool *pgxpool.Pool) DBTX {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return pool
}

// TxRunner runs a function inside a PostgreSQL transaction.
// If fn returns an error the transaction is rolled back; otherwise it is committed.
type TxRunner struct {
	pool *pgxpool.Pool
}

// NewTxRunner creates a TxRunner backed by pool.
func NewTxRunner(pool *pgxpool.Pool) *TxRunner {
	return &TxRunner{pool: pool}
}

// RunInTx begins a READ COMMITTED transaction, injects it into ctx via WithTx,
// calls fn, then commits. Any error from fn triggers a rollback.
func (r *TxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	txCtx := WithTx(ctx, tx)

	if fnErr := fn(txCtx); fnErr != nil {
		// Best-effort rollback; ignore rollback error to surface the original.
		_ = tx.Rollback(ctx)
		return fnErr
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// IsDuplicateKeyError returns true for PostgreSQL unique-constraint violations (SQLSTATE 23505).
func IsDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// NilIfEmpty returns nil when s is empty, so nullable TEXT columns store NULL rather than "".
func NilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// NilIfEmptyPtr returns nil when p is nil or points to an empty string.
func NilIfEmptyPtr(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}
