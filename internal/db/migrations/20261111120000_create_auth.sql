-- +goose Up
-- platform_role is for the people who run the service (/admin), not for
-- workspace members; most accounts have none.
CREATE TABLE accounts (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email         text        NOT NULL UNIQUE CHECK (email = lower(email)),
    name          text        NOT NULL,
    password_hash text        NOT NULL,
    platform_role text        CHECK (platform_role IN ('superadmin', 'support', 'editor')),
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

-- workspace_id gets its foreign key from the workspaces migration, so this
-- one stands alone in a project without workspaces.
CREATE TABLE audit_log (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at           timestamptz NOT NULL DEFAULT now(),
    actor_id     bigint      REFERENCES accounts (id) ON DELETE SET NULL,
    workspace_id bigint,
    action       text        NOT NULL,
    target       text        NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at ON audit_log (at DESC, id DESC);
CREATE INDEX audit_log_action ON audit_log (action, at DESC, id DESC);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE password_resets;
DROP TABLE sessions;
DROP TABLE accounts;
