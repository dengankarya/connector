-- admin_roles must be created before admin_users.role_id FK is added.
-- admin_roles.created_by → admin_users is safe because admin_users already exists.
CREATE TABLE admin_roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    created_by  UUID        REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE admin_role_permissions (
    role_id  UUID NOT NULL REFERENCES admin_roles(id) ON DELETE CASCADE,
    resource TEXT NOT NULL,
    action   TEXT NOT NULL,
    PRIMARY KEY (role_id, resource, action)
);

-- Add RBAC columns to existing admin_users table.
ALTER TABLE admin_users
    ADD COLUMN is_super_admin BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN role_id        UUID    REFERENCES admin_roles(id) ON DELETE SET NULL;
