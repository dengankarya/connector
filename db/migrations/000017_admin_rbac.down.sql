-- Drop in reverse dependency order.
ALTER TABLE admin_users
    DROP COLUMN IF EXISTS role_id,
    DROP COLUMN IF EXISTS is_super_admin;

DROP TABLE IF EXISTS admin_role_permissions;
DROP TABLE IF EXISTS admin_roles;
