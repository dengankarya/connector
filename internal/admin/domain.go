package admin

import (
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = common.NewDomainError("AU_INVALID_CREDENTIALS", "invalid email or password")
	ErrTokenInvalid       = common.NewDomainError("AU_TOKEN_INVALID", "invalid or expired token")
)

// AdminUser is the platform-operator account stored in admin_users.
type AdminUser struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	IsSuperAdmin bool       `json:"is_super_admin"`
	RoleID       *uuid.UUID `json:"role_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AdminUserSummary is the public view of an admin user (no password hash).
type AdminUserSummary struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	IsSuperAdmin bool       `json:"is_super_admin"`
	RoleID       *uuid.UUID `json:"role_id,omitempty"`
	RoleName     string     `json:"role_name,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Role is a named set of resource-action permissions.
type Role struct {
	ID          uuid.UUID    `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	CreatedBy   *uuid.UUID   `json:"created_by,omitempty"`
	Permissions []Permission `json:"permissions"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// Permission is a single resource:action grant within a role.
type Permission struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// Valid resource and action values enforced at the API layer.
var (
	ValidResources = []string{"transactions", "payouts", "merchants", "kyc"}
	ValidActions   = []string{"READ", "WRITE", "UPDATE"}
)

func isValidResource(r string) bool {
	for _, v := range ValidResources {
		if v == r {
			return true
		}
	}
	return false
}

func isValidAction(a string) bool {
	for _, v := range ValidActions {
		if v == a {
			return true
		}
	}
	return false
}

// CreateRoleBody is the request body for creating or updating a role.
type CreateRoleBody struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Permissions []Permission `json:"permissions"`
}

// CreateAdminUserBody is the request body for creating a new admin user.
type CreateAdminUserBody struct {
	Email    string     `json:"email"`
	Password string     `json:"password"`
	RoleID   *uuid.UUID `json:"role_id,omitempty"`
}

// LoginBody is the request body for POST /admin/auth/login.
type LoginBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is returned on successful login.
type LoginResponse struct {
	Token string `json:"token"`
}

// TopupBody is the request body for POST /admin/merchants/:tenantId/topup.
type TopupBody struct {
	Amount   int64  `json:"amount" example:"500000"`
	Currency string `json:"currency" example:"IDR"`
	Note     string `json:"note" example:"Manual top-up"`
}

// ShippingTopup is the created topup record returned by the admin topup endpoint.
type ShippingTopup struct {
	ID        string `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at"`
}

// AdminTransaction is a simplified payment transaction view for the admin panel.
type AdminTransaction struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       int64      `json:"tenant_id"`
	OrderNumber    string     `json:"order_number,omitempty"`
	Provider       string     `json:"provider"`
	Amount         int64      `json:"amount"`
	Currency       string     `json:"currency"`
	PlatformFee    int64      `json:"platform_fee"`
	MerchantAmount int64      `json:"merchant_amount"`
	Status         string     `json:"status"`
	PaymentMethod  string     `json:"payment_method,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	PaidAt         *time.Time `json:"paid_at,omitempty"`
}

// AdminPayout is a simplified payout view for the admin panel.
type AdminPayout struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      int64      `json:"tenant_id"`
	Provider      string     `json:"provider"`
	Amount        int64      `json:"amount"`
	Currency      string     `json:"currency"`
	Status        string     `json:"status"`
	BankCode      string     `json:"bank_code,omitempty"`
	AccountNumber string     `json:"account_number,omitempty"`
	AccountName   string     `json:"account_name,omitempty"`
	Description   string     `json:"description,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
}

// AdminPayoutFilter controls optional filters on cross-tenant payout listing.
type AdminPayoutFilter struct {
	TenantID *int64
	Status   string
}

// ValidPayoutStatuses is the allowed set of payout status values.
var ValidPayoutStatuses = map[string]bool{
	"pending": true, "processing": true, "completed": true,
	"failed": true, "cancelled": true,
}

// AdminTxnFilter controls cross-tenant transaction listing.
type AdminTxnFilter struct {
	TenantID *int64
	From     *time.Time
	To       *time.Time
	Limit    int
	Cursor   string
}

// TxnCursorPoint is the (created_at, id) keyset used for cursor pagination on transactions.
type TxnCursorPoint struct {
	CreatedAt time.Time
	ID        uuid.UUID
}
