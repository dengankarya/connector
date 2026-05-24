package dbconn

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	log "github.com/sirupsen/logrus"
)

// ConnectPgx creates and validates a pgxpool.Pool.
// This is the preferred connection for the payment module — pgx/v5 supports
// LISTEN/NOTIFY, COPY, and proper transaction semantics for SELECT FOR UPDATE.
func ConnectPgx(dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pgx config: %w", err)
	}

	cfg.MaxConns = 25
	cfg.MinConns = 5
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.MaxConnIdleTime = 2 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	log.WithFields(log.Fields{
		"max_conns":    cfg.MaxConns,
		"min_conns":    cfg.MinConns,
		"max_lifetime": cfg.MaxConnLifetime,
	}).Info("postgres (pgx) connection pool established")

	return pool, nil
}
