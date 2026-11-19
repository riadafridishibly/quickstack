-- +goose Up
-- password_hash is NULL for an account that signs in only another way, such
-- as with a passkey or through a provider. platform_role is for the people
-- who run the service (/admin), not for workspace members; most accounts
-- have none.
CREATE TABLE accounts (
    id                bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email             text        NOT NULL UNIQUE CHECK (email = lower(email)),
    email_verified_at timestamptz,
    name              text        NOT NULL,
    password_hash     text,
    platform_role     text        CHECK (platform_role IN ('superadmin', 'support', 'editor')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX accounts_created ON accounts (created_at DESC, id DESC);

-- Only a hash of the token is stored, so a leaked table can't sign anyone in.
-- aal is the assurance level: 1 after one factor, 2 after two.
-- authenticated_at is when the user last proved who they are, which
-- confirm-it's-you checks before sensitive changes.
CREATE TABLE sessions (
    token_hash       bytea       PRIMARY KEY,
    id               bigint      NOT NULL GENERATED ALWAYS AS IDENTITY UNIQUE,
    account_id       bigint      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    aal              smallint    NOT NULL CHECK (aal IN (1, 2)),
    user_agent       text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    authenticated_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at     timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL
);
CREATE INDEX sessions_account ON sessions (account_id);

CREATE TABLE password_resets (
    token_hash bytea       PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX password_resets_account ON password_resets (account_id);

-- email is the address the link went to; it verifies only while the account
-- still has that address.
CREATE TABLE email_verifications (
    token_hash bytea       PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    email      text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX email_verifications_account ON email_verifications (account_id);

-- A login challenge is what someone holds between the first factor and the
-- second: not a session, so it opens nothing.
CREATE TABLE login_challenges (
    token_hash bytea       PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    next       text        NOT NULL,
    attempts   integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX login_challenges_account ON login_challenges (account_id);

-- Recovery codes stand in for a second factor that is lost. Each works once.
-- They are random enough that a plain hash protects them.
CREATE TABLE recovery_codes (
    account_id bigint      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    code_hash  bytea       NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, code_hash)
);

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
DROP TABLE recovery_codes;
DROP TABLE login_challenges;
DROP TABLE email_verifications;
DROP TABLE password_resets;
DROP TABLE sessions;
DROP TABLE accounts;
