package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dengankarya/connector/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbtx = postgres.DBTX

func dbFromContext(ctx context.Context, pool *pgxpool.Pool) dbtx {
	return postgres.DBFromContext(ctx, pool)
}

// Repository handles admin_users DB queries and cross-tenant read queries.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a Repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// GetByEmail fetches an admin user by email address.
// Returns ErrInvalidCredentials when not found (collapses not-found and wrong-password for timing safety).
func (r *Repository) GetByEmail(ctx context.Context, email string) (*AdminUser, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, email, password_hash, is_super_admin, role_id::text, created_at, updated_at
		FROM admin_users
		WHERE email = $1`, email)

	var (
		u      AdminUser
		idStr  string
		roleID *string
	)
	if err := row.Scan(&idStr, &u.Email, &u.PasswordHash, &u.IsSuperAdmin, &roleID, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("get admin user by email: %w", err)
	}
	u.ID, _ = uuid.Parse(idStr)
	if roleID != nil {
		id, _ := uuid.Parse(*roleID)
		u.RoleID = &id
	}
	return &u, nil
}

// GetUserPermissions returns the super-admin flag and permission strings for a user.
// Permission strings are formatted as "resource:ACTION" (e.g. "transactions:READ").
func (r *Repository) GetUserPermissions(ctx context.Context, userID uuid.UUID) (isSuperAdmin bool, perms []string, err error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT u.is_super_admin, rp.resource, rp.action
		FROM admin_users u
		LEFT JOIN admin_roles ar ON ar.id = u.role_id
		LEFT JOIN admin_role_permissions rp ON rp.role_id = ar.id
		WHERE u.id = $1`, userID)
	if err != nil {
		return false, nil, fmt.Errorf("get user permissions: %w", err)
	}
	defer rows.Close()

	first := true
	for rows.Next() {
		var resource, action *string
		if err := rows.Scan(&isSuperAdmin, &resource, &action); err != nil {
			return false, nil, fmt.Errorf("scan user permission: %w", err)
		}
		first = false
		if resource != nil && action != nil {
			perms = append(perms, *resource+":"+*action)
		}
	}
	if first {
		// user not found — fallback to safe defaults
		return false, nil, nil
	}
	return isSuperAdmin, perms, rows.Err()
}

// GetByID fetches an admin user by primary key.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*AdminUser, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, email, password_hash, is_super_admin, role_id::text, created_at, updated_at
		FROM admin_users
		WHERE id = $1`, id)

	var (
		u      AdminUser
		idStr  string
		roleID *string
	)
	if err := row.Scan(&idStr, &u.Email, &u.PasswordHash, &u.IsSuperAdmin, &roleID, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("get admin user by id: %w", err)
	}
	u.ID, _ = uuid.Parse(idStr)
	if roleID != nil {
		rid, _ := uuid.Parse(*roleID)
		u.RoleID = &rid
	}
	return &u, nil
}

// Create inserts a new admin user.
func (r *Repository) Create(ctx context.Context, u *AdminUser) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	now := time.Now().UTC()
	u.CreatedAt = now
	u.UpdatedAt = now

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO admin_users (id, email, password_hash, is_super_admin, role_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		u.ID, u.Email, u.PasswordHash, u.IsSuperAdmin, u.RoleID, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert admin_user: %w", err)
	}
	return nil
}

// ─── Role management ──────────────────────────────────────────────────────────

// ListRoles returns all roles with their permissions.
func (r *Repository) ListRoles(ctx context.Context) ([]*Role, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			ar.id::text, ar.name, ar.description,
			ar.created_by::text, ar.created_at, ar.updated_at,
			rp.resource, rp.action
		FROM admin_roles ar
		LEFT JOIN admin_role_permissions rp ON rp.role_id = ar.id
		ORDER BY ar.name, rp.resource, rp.action`)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()

	roleMap := map[string]*Role{}
	var order []string

	for rows.Next() {
		var (
			idStr     string
			name      string
			desc      string
			createdBy *string
			createdAt time.Time
			updatedAt time.Time
			resource  *string
			action    *string
		)
		if err := rows.Scan(&idStr, &name, &desc, &createdBy, &createdAt, &updatedAt, &resource, &action); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		if _, exists := roleMap[idStr]; !exists {
			role := &Role{Name: name, Description: desc, CreatedAt: createdAt, UpdatedAt: updatedAt}
			role.ID, _ = uuid.Parse(idStr)
			if createdBy != nil {
				id, _ := uuid.Parse(*createdBy)
				role.CreatedBy = &id
			}
			roleMap[idStr] = role
			order = append(order, idStr)
		}
		if resource != nil && action != nil {
			roleMap[idStr].Permissions = append(roleMap[idStr].Permissions, Permission{
				Resource: *resource, Action: *action,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]*Role, 0, len(order))
	for _, id := range order {
		result = append(result, roleMap[id])
	}
	return result, nil
}

// GetRole fetches a single role with its permissions.
func (r *Repository) GetRole(ctx context.Context, id uuid.UUID) (*Role, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			ar.id::text, ar.name, ar.description,
			ar.created_by::text, ar.created_at, ar.updated_at,
			rp.resource, rp.action
		FROM admin_roles ar
		LEFT JOIN admin_role_permissions rp ON rp.role_id = ar.id
		WHERE ar.id = $1
		ORDER BY rp.resource, rp.action`, id)
	if err != nil {
		return nil, fmt.Errorf("get role: %w", err)
	}
	defer rows.Close()

	var role *Role
	for rows.Next() {
		var (
			idStr     string
			name      string
			desc      string
			createdBy *string
			createdAt time.Time
			updatedAt time.Time
			resource  *string
			action    *string
		)
		if err := rows.Scan(&idStr, &name, &desc, &createdBy, &createdAt, &updatedAt, &resource, &action); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		if role == nil {
			role = &Role{Name: name, Description: desc, CreatedAt: createdAt, UpdatedAt: updatedAt}
			role.ID, _ = uuid.Parse(idStr)
			if createdBy != nil {
				cbID, _ := uuid.Parse(*createdBy)
				role.CreatedBy = &cbID
			}
		}
		if resource != nil && action != nil {
			role.Permissions = append(role.Permissions, Permission{Resource: *resource, Action: *action})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if role == nil {
		return nil, fmt.Errorf("role not found")
	}
	return role, nil
}

// CreateRole inserts a new role and its permissions in a single transaction.
func (r *Repository) CreateRole(ctx context.Context, body CreateRoleBody, createdBy uuid.UUID) (*Role, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	roleID := uuid.New()
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		INSERT INTO admin_roles (id, name, description, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		roleID, body.Name, body.Description, createdBy, now)
	if err != nil {
		return nil, fmt.Errorf("insert role: %w", err)
	}

	if err := insertPermissions(ctx, tx, roleID, body.Permissions); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit role: %w", err)
	}
	return r.GetRole(ctx, roleID)
}

// UpdateRole replaces a role's name, description, and permissions atomically.
func (r *Repository) UpdateRole(ctx context.Context, id uuid.UUID, body CreateRoleBody) (*Role, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		UPDATE admin_roles SET name = $2, description = $3, updated_at = NOW()
		WHERE id = $1`, id, body.Name, body.Description)
	if err != nil {
		return nil, fmt.Errorf("update role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("role not found")
	}

	_, err = tx.Exec(ctx, `DELETE FROM admin_role_permissions WHERE role_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("delete permissions: %w", err)
	}

	if err := insertPermissions(ctx, tx, id, body.Permissions); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit role update: %w", err)
	}
	return r.GetRole(ctx, id)
}

// DeleteRole removes a role (permissions cascade).
func (r *Repository) DeleteRole(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM admin_roles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("role not found")
	}
	return nil
}

// insertPermissions bulk-inserts role permissions into a transaction.
func insertPermissions(ctx context.Context, tx pgx.Tx, roleID uuid.UUID, perms []Permission) error {
	for _, p := range perms {
		if _, err := tx.Exec(ctx, `
			INSERT INTO admin_role_permissions (role_id, resource, action)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`,
			roleID, p.Resource, p.Action); err != nil {
			return fmt.Errorf("insert permission %s:%s: %w", p.Resource, p.Action, err)
		}
	}
	return nil
}

// ─── User management ──────────────────────────────────────────────────────────

// ListUsers returns all admin users with their role name.
func (r *Repository) ListUsers(ctx context.Context) ([]*AdminUserSummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id::text, u.email, u.is_super_admin, u.role_id::text, COALESCE(ar.name, ''), u.created_at
		FROM admin_users u
		LEFT JOIN admin_roles ar ON ar.id = u.role_id
		ORDER BY u.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()

	var result []*AdminUserSummary
	for rows.Next() {
		var (
			u      AdminUserSummary
			idStr  string
			roleID *string
		)
		if err := rows.Scan(&idStr, &u.Email, &u.IsSuperAdmin, &roleID, &u.RoleName, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan admin user: %w", err)
		}
		u.ID, _ = uuid.Parse(idStr)
		if roleID != nil {
			id, _ := uuid.Parse(*roleID)
			u.RoleID = &id
		}
		result = append(result, &u)
	}
	return result, rows.Err()
}

// CreateUser inserts a new (non-super-admin) admin user.
func (r *Repository) CreateUser(ctx context.Context, email, passwordHash string, roleID *uuid.UUID) (*AdminUserSummary, error) {
	var (
		u      AdminUserSummary
		idStr  string
		rID    *string
	)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO admin_users (email, password_hash, is_super_admin, role_id)
		VALUES ($1, $2, false, $3)
		RETURNING id::text, email, is_super_admin, role_id::text, created_at`,
		email, passwordHash, roleID,
	).Scan(&idStr, &u.Email, &u.IsSuperAdmin, &rID, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create admin user: %w", err)
	}
	u.ID, _ = uuid.Parse(idStr)
	if rID != nil {
		id, _ := uuid.Parse(*rID)
		u.RoleID = &id
	}
	// Fetch role name separately if role was assigned.
	if u.RoleID != nil {
		_ = r.pool.QueryRow(ctx, `SELECT name FROM admin_roles WHERE id = $1`, u.RoleID).Scan(&u.RoleName)
	}
	return &u, nil
}

// UpdatePassword sets a new bcrypt-hashed password for any admin user.
func (r *Repository) UpdatePassword(ctx context.Context, id uuid.UUID, hashedPassword string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE admin_users SET password_hash = $2, updated_at = NOW() WHERE id = $1`, id, hashedPassword)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// UpdateUser updates the role assignment for an admin user.
func (r *Repository) UpdateUser(ctx context.Context, id uuid.UUID, roleID *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE admin_users SET role_id = $2, updated_at = NOW() WHERE id = $1 AND is_super_admin = false`, id, roleID)
	if err != nil {
		return fmt.Errorf("update admin user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found or is a super admin")
	}
	return nil
}

// DeleteUser removes an admin user. Super admins cannot be deleted via the API.
func (r *Repository) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM admin_users WHERE id = $1 AND is_super_admin = false`, id)
	if err != nil {
		return fmt.Errorf("delete admin user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found or is a super admin")
	}
	return nil
}

// ─── Cross-tenant queries ────────────────────────────────────────────────────

func scanAdminTransaction(row pgx.Row) (*AdminTransaction, error) {
	var (
		t                       AdminTransaction
		idStr                   string
		paidAt, processedAt     *time.Time
		platformFee, merchantAmount *int64
	)
	if err := row.Scan(
		&idStr, &t.TenantID, &t.Type,
		&t.Amount, &t.Currency, &t.Description, &t.Status,
		&t.OrderNumber, &t.Provider, &t.PaymentMethod,
		&platformFee, &merchantAmount,
		&paidAt,
		&t.BankCode, &t.AccountNumber, &t.AccountName, &t.FailureReason,
		&processedAt,
		&t.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan admin transaction: %w", err)
	}
	t.ID, _ = uuid.Parse(idStr)
	t.PaidAt = paidAt
	t.ProcessedAt = processedAt
	if platformFee != nil {
		t.PlatformFee = *platformFee
	}
	if merchantAmount != nil {
		t.MerchantAmount = *merchantAmount
	}
	return &t, nil
}

const adminTxnSelect = `
	id::text, tenant_id, type,
	amount, currency, COALESCE(description, ''), status,
	COALESCE(order_number, ''), COALESCE(provider, ''), COALESCE(payment_method, ''),
	platform_fee, merchant_amount,
	paid_at,
	COALESCE(bank_code, ''), COALESCE(account_number, ''), COALESCE(account_name, ''), COALESCE(failure_reason, ''),
	processed_at,
	created_at`

// ListTransactions returns cross-tenant transactions (all types), newest first, with offset pagination.
func (r *Repository) ListTransactions(ctx context.Context, f AdminTxnFilter) ([]*AdminTransaction, error) {
	var (
		args  []any
		where []string
	)

	if f.TenantID != nil {
		args = append(args, *f.TenantID)
		where = append(where, fmt.Sprintf("tenant_id = $%d", len(args)))
	}
	if f.Type != "" {
		args = append(args, f.Type)
		where = append(where, fmt.Sprintf("type = $%d", len(args)))
	}
	if f.From != nil {
		args = append(args, *f.From)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		where = append(where, fmt.Sprintf("created_at < $%d", len(args)))
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	q := fmt.Sprintf(
		`SELECT %s FROM transactions %s ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`,
		adminTxnSelect, whereClause, len(args)-1, len(args),
	)

	rows, err := dbFromContext(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list admin transactions: %w", err)
	}
	defer rows.Close()

	var result []*AdminTransaction
	for rows.Next() {
		t, err := scanAdminTransaction(rows)
		if err != nil {
			return nil, err
		}
		if t != nil {
			result = append(result, t)
		}
	}
	return result, rows.Err()
}

// ListPayouts returns payout records across all tenants, newest first, with optional filters and offset pagination.
func (r *Repository) ListPayouts(ctx context.Context, limit, offset int, f AdminPayoutFilter) ([]*AdminPayout, error) {
	args := []any{limit, offset}
	where := []string{"type = 'payout'"}
	if f.TenantID != nil {
		args = append(args, *f.TenantID)
		where = append(where, fmt.Sprintf("tenant_id = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}

	q := fmt.Sprintf(
		`SELECT %s FROM transactions WHERE %s ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		adminTxnSelect, strings.Join(where, " AND "),
	)

	rows, err := dbFromContext(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list admin payouts: %w", err)
	}
	defer rows.Close()

	var result []*AdminPayout
	for rows.Next() {
		p, err := scanAdminTransaction(rows)
		if err != nil {
			return nil, err
		}
		if p != nil {
			result = append(result, p)
		}
	}
	return result, rows.Err()
}
