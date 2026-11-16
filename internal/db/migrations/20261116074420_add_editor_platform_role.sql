-- +goose Up
-- An editor writes the site's blog and sees nothing else in /admin.
ALTER TABLE accounts
    DROP CONSTRAINT accounts_platform_role_check,
    ADD CONSTRAINT accounts_platform_role_check CHECK (platform_role IN ('superadmin', 'support', 'editor'));

-- +goose Down
UPDATE accounts SET platform_role = NULL WHERE platform_role = 'editor';
ALTER TABLE accounts
    DROP CONSTRAINT accounts_platform_role_check,
    ADD CONSTRAINT accounts_platform_role_check CHECK (platform_role IN ('superadmin', 'support'));
