-- +goose Up
-- These tables have no row-level security: they are read before a workspace
-- is known (by slug, by invitation token) or across workspaces (an account's
-- memberships, the platform admin). Queries filter by workspace_id themselves.
CREATE TABLE workspaces (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug       text        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name       text        NOT NULL,
    public     boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workspaces_created ON workspaces (created_at DESC, id DESC);

CREATE TABLE memberships (
    workspace_id bigint      NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    account_id   bigint      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    role         text        NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, account_id)
);
CREATE INDEX memberships_account ON memberships (account_id);

CREATE TABLE invitations (
    workspace_id bigint      NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    id           bigint      GENERATED ALWAYS AS IDENTITY,
    email        text        NOT NULL CHECK (email = lower(email)),
    role         text        NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
    token_hash   bytea       NOT NULL UNIQUE,
    invited_by   bigint      REFERENCES accounts (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, email)
);

CREATE TABLE audit_log (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at           timestamptz NOT NULL DEFAULT now(),
    actor_id     bigint      REFERENCES accounts (id) ON DELETE SET NULL,
    workspace_id bigint      REFERENCES workspaces (id) ON DELETE SET NULL,
    action       text        NOT NULL,
    target       text        NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at ON audit_log (at DESC, id DESC);
CREATE INDEX audit_log_action ON audit_log (action, at DESC, id DESC);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE invitations;
DROP TABLE memberships;
DROP TABLE workspaces;
