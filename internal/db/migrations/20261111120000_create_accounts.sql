-- +goose Up
ALTER DEFAULT PRIVILEGES FOR ROLE quickstack_owner
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO quickstack_app;
ALTER DEFAULT PRIVILEGES FOR ROLE quickstack_owner
    GRANT USAGE, SELECT ON SEQUENCES TO quickstack_app;

-- platform_role is for the people who run the service (/admin), not for
-- workspace members; most accounts have none.
CREATE TABLE accounts (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email         text        NOT NULL UNIQUE CHECK (email = lower(email)),
    name          text        NOT NULL,
    password_hash text        NOT NULL,
    platform_role text        CHECK (platform_role IN ('superadmin', 'support')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX accounts_created ON accounts (created_at DESC, id DESC);

-- Only a hash of the token is stored, so a leaked table can't sign anyone in.
CREATE TABLE sessions (
    token_hash   bytea       PRIMARY KEY,
    id           bigint      NOT NULL GENERATED ALWAYS AS IDENTITY UNIQUE,
    account_id   bigint      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_agent   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_account ON sessions (account_id);

CREATE TABLE password_resets (
    token_hash bytea       PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX password_resets_account ON password_resets (account_id);

-- +goose Down
DROP TABLE password_resets;
DROP TABLE sessions;
DROP TABLE accounts;
ALTER DEFAULT PRIVILEGES FOR ROLE quickstack_owner
    REVOKE USAGE, SELECT ON SEQUENCES FROM quickstack_app;
ALTER DEFAULT PRIVILEGES FOR ROLE quickstack_owner
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM quickstack_app;
