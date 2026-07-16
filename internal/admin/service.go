package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/internal/account"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// Service handles admin authentication and cross-tenant data access.
type Service struct {
	repo       *Repository
	accountSvc *account.Service
	secret     string
	logger     *logrus.Logger
}

// NewService creates a Service.
func NewService(repo *Repository, accountSvc *account.Service, secret string, logger *logrus.Logger) *Service {
	return &Service{repo: repo, accountSvc: accountSvc, secret: secret, logger: logger}
}

// ─── Auth ────────────────────────────────────────────────────────────────────

// Login validates credentials and returns a signed JWT.
func (s *Service) Login(ctx context.Context, email, password string) (*LoginResponse, error) {
	user, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return nil, err // already ErrInvalidCredentials on not-found
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	token, err := IssueToken(user.ID, s.secret)
	if err != nil {
		return nil, fmt.Errorf("login: issue token: %w", err)
	}
	s.logger.WithField("admin_id", user.ID).Info("admin login")
	return &LoginResponse{Token: token}, nil
}

// ─── Cross-tenant transactions ────────────────────────────────────────────────

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// ListTransactions returns cross-tenant transactions with optional cursor pagination.
func (s *Service) ListTransactions(ctx context.Context, f AdminTxnFilter) (*common.PaginationResponse[*AdminTransaction], error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultPageSize
	} else if limit > maxPageSize {
		limit = maxPageSize
	}

	var cursor *TxnCursorPoint
	if f.Cursor != "" {
		var err error
		cursor, err = decodeTxnCursor(f.Cursor)
		if err != nil {
			return nil, common.NewDomainError("BR_INVALID_CURSOR", "invalid pagination cursor")
		}
	}

	f.Limit = limit + 1 // fetch one extra to detect has_more
	items, err := s.repo.ListTransactions(ctx, f, cursor)
	if err != nil {
		return nil, err
	}

	result := &common.PaginationResponse[*AdminTransaction]{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		result.HasMore = true
		last := result.Items[limit-1]
		result.NextCursor = encodeTxnCursor(last.CreatedAt, last.ID)
	}
	return result, nil
}

// ─── Payouts ──────────────────────────────────────────────────────────────────

// ListPayouts returns all payouts across all tenants with offset pagination.
func (s *Service) ListPayouts(ctx context.Context, limit, offset int) ([]*AdminPayout, error) {
	if limit <= 0 {
		limit = defaultPageSize
	} else if limit > maxPageSize {
		limit = maxPageSize
	}
	return s.repo.ListPayouts(ctx, limit, offset)
}

// ─── Topup ────────────────────────────────────────────────────────────────────

// Topup credits a merchant's shipping balance. Delegates to account.Service to reuse
// the transactional logic (CreditAvailable + CreateTopup in one DB transaction).
func (s *Service) Topup(ctx context.Context, tenantID int64, amount int64, currency, note string) (*account.ShippingTopup, error) {
	if currency == "" {
		currency = "IDR"
	}
	return s.accountSvc.Topup(ctx, account.TopupRequest{
		TenantID: tenantID,
		Amount:   amount,
		Currency: currency,
		Note:     note,
	})
}

// ─── Cursor encoding ──────────────────────────────────────────────────────────

type txnCursorPayload struct {
	T time.Time `json:"t"`
	I string    `json:"i"`
}

func encodeTxnCursor(createdAt time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(txnCursorPayload{T: createdAt.UTC(), I: id.String()})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeTxnCursor(s string) (*TxnCursorPoint, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var p txnCursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	id, err := uuid.Parse(p.I)
	if err != nil {
		return nil, fmt.Errorf("parse uuid: %w", err)
	}
	return &TxnCursorPoint{CreatedAt: p.T, ID: id}, nil
}
