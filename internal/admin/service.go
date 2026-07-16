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

// Login validates credentials and returns a signed JWT with the user's permissions embedded.
func (s *Service) Login(ctx context.Context, email, password string) (*LoginResponse, error) {
	user, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return nil, err // already ErrInvalidCredentials on not-found
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	isSuperAdmin, perms, err := s.repo.GetUserPermissions(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("login: fetch permissions: %w", err)
	}
	token, err := IssueToken(user.ID, isSuperAdmin, perms, s.secret)
	if err != nil {
		return nil, fmt.Errorf("login: issue token: %w", err)
	}
	s.logger.WithField("admin_id", user.ID).Info("admin login")
	return &LoginResponse{Token: token}, nil
}

// ─── Roles ────────────────────────────────────────────────────────────────────

// ListRoles returns all roles with their permissions.
func (s *Service) ListRoles(ctx context.Context) ([]*Role, error) {
	return s.repo.ListRoles(ctx)
}

// CreateRole creates a new role with the given permissions.
func (s *Service) CreateRole(ctx context.Context, body CreateRoleBody, createdBy uuid.UUID) (*Role, error) {
	if body.Name == "" {
		return nil, common.NewDomainError("RB_INVALID", "role name is required")
	}
	for _, p := range body.Permissions {
		if !isValidResource(p.Resource) {
			return nil, common.NewDomainError("RB_INVALID", "invalid resource: "+p.Resource)
		}
		if !isValidAction(p.Action) {
			return nil, common.NewDomainError("RB_INVALID", "invalid action: "+p.Action)
		}
	}
	return s.repo.CreateRole(ctx, body, createdBy)
}

// UpdateRole replaces a role's name, description, and permissions.
func (s *Service) UpdateRole(ctx context.Context, id uuid.UUID, body CreateRoleBody) (*Role, error) {
	if body.Name == "" {
		return nil, common.NewDomainError("RB_INVALID", "role name is required")
	}
	for _, p := range body.Permissions {
		if !isValidResource(p.Resource) {
			return nil, common.NewDomainError("RB_INVALID", "invalid resource: "+p.Resource)
		}
		if !isValidAction(p.Action) {
			return nil, common.NewDomainError("RB_INVALID", "invalid action: "+p.Action)
		}
	}
	return s.repo.UpdateRole(ctx, id, body)
}

// DeleteRole removes a role.
func (s *Service) DeleteRole(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteRole(ctx, id)
}

// ─── Admin Users ──────────────────────────────────────────────────────────────

// ListUsers returns all admin users.
func (s *Service) ListUsers(ctx context.Context) ([]*AdminUserSummary, error) {
	return s.repo.ListUsers(ctx)
}

// CreateUser creates a new non-super-admin user with an optional role.
func (s *Service) CreateUser(ctx context.Context, body CreateAdminUserBody) (*AdminUserSummary, error) {
	if body.Email == "" || body.Password == "" {
		return nil, common.NewDomainError("RB_INVALID", "email and password are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	return s.repo.CreateUser(ctx, body.Email, string(hash), body.RoleID)
}

// UpdateUser changes the role assigned to a user.
func (s *Service) UpdateUser(ctx context.Context, id uuid.UUID, roleID *uuid.UUID) error {
	return s.repo.UpdateUser(ctx, id, roleID)
}

// ChangePassword verifies the user's current password then stores the new one.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, oldPassword, newPassword string) error {
	if newPassword == "" {
		return common.NewDomainError("RB_INVALID", "new password cannot be empty")
	}
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)); err != nil {
		return ErrInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.repo.UpdatePassword(ctx, userID, string(hash))
}

// DeleteUser removes a non-super-admin user.
func (s *Service) DeleteUser(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteUser(ctx, id)
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
