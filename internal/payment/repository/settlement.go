package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettlementSnapshotRepository manages merchant_settlement_snapshots rows.
type SettlementSnapshotRepository struct {
	pool *pgxpool.Pool
}

// NewSettlementSnapshotRepository creates a SettlementSnapshotRepository.
func NewSettlementSnapshotRepository(pool *pgxpool.Pool) *SettlementSnapshotRepository {
	return &SettlementSnapshotRepository{pool: pool}
}

// GetByTenantID fetches the snapshot for a tenant. Returns domain.ErrNotFound when no snapshot exists yet.
func (r *SettlementSnapshotRepository) GetByTenantID(ctx context.Context, tenantID int64) (*domain.MerchantSettlementSnapshot, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, tenant_id,
		       pending_balance, pending_platform_fee,
		       pending_xendit_fee, pending_vat, pending_withholding,
		       settled_balance, settled_platform_fee,
		       settled_xendit_fee, settled_vat, settled_withholding,
		       last_synced_at, created_at, updated_at
		FROM merchant_settlement_snapshots
		WHERE tenant_id = $1`, tenantID)
	return scanSnapshot(row)
}

// Upsert inserts or updates the snapshot for a tenant based on tenant_id uniqueness.
func (r *SettlementSnapshotRepository) Upsert(ctx context.Context, s *domain.MerchantSettlementSnapshot) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	now := time.Now().UTC()
	s.UpdatedAt = now
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO merchant_settlement_snapshots (
			id, tenant_id,
			pending_balance, pending_platform_fee,
			pending_xendit_fee, pending_vat, pending_withholding,
			settled_balance, settled_platform_fee,
			settled_xendit_fee, settled_vat, settled_withholding,
			last_synced_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (tenant_id) DO UPDATE SET
			pending_balance      = EXCLUDED.pending_balance,
			pending_platform_fee = EXCLUDED.pending_platform_fee,
			pending_xendit_fee   = EXCLUDED.pending_xendit_fee,
			pending_vat          = EXCLUDED.pending_vat,
			pending_withholding  = EXCLUDED.pending_withholding,
			settled_balance      = EXCLUDED.settled_balance,
			settled_platform_fee = EXCLUDED.settled_platform_fee,
			settled_xendit_fee   = EXCLUDED.settled_xendit_fee,
			settled_vat          = EXCLUDED.settled_vat,
			settled_withholding  = EXCLUDED.settled_withholding,
			last_synced_at       = EXCLUDED.last_synced_at,
			updated_at           = EXCLUDED.updated_at`,
		s.ID, s.TenantID,
		s.PendingBalance, s.PendingPlatformFee,
		s.PendingXenditFee, s.PendingVAT, s.PendingWithholding,
		s.SettledBalance, s.SettledPlatformFee,
		s.SettledXenditFee, s.SettledVAT, s.SettledWithholding,
		s.LastSyncedAt, s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert settlement snapshot for tenant %d: %w", s.TenantID, err)
	}
	return nil
}

func scanSnapshot(row pgx.Row) (*domain.MerchantSettlementSnapshot, error) {
	var (
		s            domain.MerchantSettlementSnapshot
		idStr        string
		lastSyncedAt *time.Time
	)
	err := row.Scan(
		&idStr, &s.TenantID,
		&s.PendingBalance, &s.PendingPlatformFee,
		&s.PendingXenditFee, &s.PendingVAT, &s.PendingWithholding,
		&s.SettledBalance, &s.SettledPlatformFee,
		&s.SettledXenditFee, &s.SettledVAT, &s.SettledWithholding,
		&lastSyncedAt, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound{Entity: "merchant_settlement_snapshot"}
		}
		return nil, fmt.Errorf("scan settlement snapshot: %w", err)
	}
	s.ID, _ = uuid.Parse(idStr)
	s.LastSyncedAt = lastSyncedAt
	return &s, nil
}
