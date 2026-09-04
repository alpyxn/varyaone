-- Self-update was removed, so the permission that gated it grants nothing.
--
-- The baseline seed no longer creates it, which covers a fresh installation.
-- This drops it from installations that already have it, along with any role
-- that was granted it, so the roles screen stops offering a permission with
-- nothing behind it.
DELETE FROM role_permissions WHERE permission_code = 'system.update.manage';
DELETE FROM permissions WHERE code = 'system.update.manage';
