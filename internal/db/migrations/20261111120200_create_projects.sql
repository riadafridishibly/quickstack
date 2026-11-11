-- +goose Up
CREATE TABLE projects (
    workspace_id bigint      NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    id           bigint      GENERATED ALWAYS AS IDENTITY,
    name         text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    status       text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    -- A public project shows on the workspace's public roadmap, if the
    -- workspace is public too.
    public       boolean     NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, id)
);
CREATE INDEX projects_workspace_updated ON projects (workspace_id, updated_at DESC, id DESC);

CREATE TABLE tasks (
    workspace_id bigint      NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    id           bigint      GENERATED ALWAYS AS IDENTITY,
    project_id   bigint      NOT NULL,
    title        text        NOT NULL,
    status       text        NOT NULL DEFAULT 'todo' CHECK (status IN ('todo', 'doing', 'done')),
    position     int         NOT NULL DEFAULT 0,
    assignee_id  bigint,
    due_on       date,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (workspace_id, id),
    FOREIGN KEY (workspace_id, project_id) REFERENCES projects (workspace_id, id) ON DELETE CASCADE,
    -- Removing a member unassigns their tasks.
    FOREIGN KEY (workspace_id, assignee_id) REFERENCES memberships (workspace_id, account_id)
        ON DELETE SET NULL (assignee_id),
    CHECK ((status = 'done') = (completed_at IS NOT NULL))
);
CREATE INDEX tasks_project ON tasks (workspace_id, project_id, status, position);
CREATE INDEX tasks_assignee ON tasks (workspace_id, assignee_id) WHERE assignee_id IS NOT NULL;
CREATE INDEX tasks_created ON tasks (workspace_id, created_at);
CREATE INDEX tasks_completed ON tasks (workspace_id, completed_at) WHERE completed_at IS NOT NULL;

ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE projects FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON projects
    USING (workspace_id = current_setting('app.workspace_id')::bigint)
    WITH CHECK (workspace_id = current_setting('app.workspace_id')::bigint);

ALTER TABLE tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON tasks
    USING (workspace_id = current_setting('app.workspace_id')::bigint)
    WITH CHECK (workspace_id = current_setting('app.workspace_id')::bigint);

-- +goose Down
DROP TABLE tasks;
DROP TABLE projects;
