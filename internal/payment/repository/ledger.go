package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LedgerRepository manages ledger_entries rows.
// Entries are append-only — no Update or Delete methods are provided by design.
type LedgerRepository struct {
	pool *pgxpool.Pool
}

// NewLedgerRepository creates a LedgerRepository.
func NewLedgerRepository(pool *pgxpool.Pool) *LedgerRepository {
	return &LedgerRepository{pool: pool}
}

// CreateEntries atomically inserts a slice of ledger entries.
// Must be called inside a DB transaction (ctx must carry a pgx.Tx via WithTx).
// Returns domain.ErrDuplicateLedgerEntry on reference_id conflict (safe to swallow for idempotency).
func (r *LedgerRepository) CreateEntries(ctx context.Context, entries []domain.LedgerEntry) error {
	db := dbFromContext(ctx, r.pool)

	for _, e := range entries {
		if e.ID == uuid.Nil {
			e.ID = uuid.New()
		}
		e.CreatedAt = time.Now().UTC()

		metaJSON, metaValid := marshalJSON(e.Metadata)

		var webhookEventIDStr *string
		if e.WebhookEventID != nil {
			s := e.WebhookEventID.String()
			webhookEventIDStr = &s
		}

		q := `
			INSERT INTO ledger_entries (
				id, tenant_id, transaction_id, webhook_event_id,
				account_type, direction, amount, currency,
				reference_id, description, metadata,
				created_at
			) VALUES (
				$1, $2, $3, $4::uuid,
				$5, $6, $7, $8,
				$9, $10, $11::jsonb,
				$12
			)
			ON CONFLICT (tenant_id, reference_id) DO NOTHING`

		tag, err := db.Exec(ctx, q,
			e.ID, e.TenantID, e.TransactionID, webhookEventIDStr,
			string(e.AccountType), string(e.Direction), e.Amount, e.Currency,
			e.ReferenceID, e.Description, jsonParam(metaJSON, metaValid),
			e.CreatedAt,
		)
		if err != nil {
			if isDuplicateKeyError(err) {
				return domain.ErrDuplicateLedgerEntry
			}
			return fmt.Errorf("insert ledger_entry (ref=%s): %w", e.ReferenceID, err)
		}
		if tag.RowsAffected() == 0 {
			// ON CONFLICT DO NOTHING — entry already exists; this is safe/idempotent
			continue
		}
	}
	return nil
}

// GetByTransactionID returns all ledger entries for a transaction, ordered by creation time.
func (r *LedgerRepository) GetByTransactionID(ctx context.Context, txID uuid.UUID) ([]domain.LedgerEntry, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT
			id::text, tenant_id, transaction_id::text,
			webhook_event_id::text,
			account_type, direction, amount, currency,
			reference_id, description,
			metadata,
			created_at
		FROM ledger_entries
		WHERE transaction_id = $1
		ORDER BY created_at`, txID)
	if err != nil {
		return nil, fmt.Errorf("query ledger by transaction: %w", err)
	}
	defer rows.Close()

	var entries []domain.LedgerEntry
	for rows.Next() {
		var (
			e                      domain.LedgerEntry
			idStr, txnIDStr        string
			webhookIDStr           *string
			accountType, direction string
			metaBytes              []byte
		)
		err := rows.Scan(
			&idStr, &e.TenantID, &txnIDStr,
			&webhookIDStr,
			&accountType, &direction, &e.Amount, &e.Currency,
			&e.ReferenceID, &e.Description,
			&metaBytes,
			&e.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan ledger_entry: %w", err)
		}
		e.ID = mustParseUUID(idStr)
		e.TransactionID = mustParseUUID(txnIDStr)
		e.WebhookEventID = parseOptionalUUID(webhookIDStr)
		e.AccountType = domain.LedgerAccountType(accountType)
		e.Direction = domain.LedgerDirection(direction)
		_ = unmarshalJSON(metaBytes, &e.Metadata)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// SumByAccountType returns the net balance for an account type within a tenant.
// Positive = credit surplus; negative = debit surplus.
func (r *LedgerRepository) SumByAccountType(ctx context.Context, tenantID int64, accountType domain.LedgerAccountType) (int64, error) {
	var sum int64
	err := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT COALESCE(
			SUM(CASE direction WHEN 'credit' THEN amount ELSE -amount END), 0
		)
		FROM ledger_entries
		WHERE tenant_id = $1 AND account_type = $2`,
		tenantID, string(accountType)).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("sum ledger %s: %w", accountType, err)
	}
	return sum, nil
}
