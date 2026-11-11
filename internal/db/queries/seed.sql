-- Queries only internal/seed uses, to backdate sample data.

-- name: SeedTask :one
INSERT INTO tasks (workspace_id, project_id, title, status, position, assignee_id, due_on,
                   created_at, updated_at, completed_at)
VALUES (@workspace_id, @project_id, @title, @status, @position, sqlc.narg(assignee_id), sqlc.narg(due_on),
        @created_at, @created_at, sqlc.narg(completed_at))
RETURNING id;

-- name: SeedAccountCreatedAt :exec
UPDATE accounts SET created_at = @created_at WHERE id = @id;

-- name: SeedWorkspaceCreatedAt :exec
UPDATE workspaces SET created_at = @created_at WHERE id = @id;

-- name: SeedDeleteWorkspace :exec
DELETE FROM workspaces WHERE slug = @slug;

-- name: SeedDeleteAccount :exec
DELETE FROM accounts WHERE email = @email;
