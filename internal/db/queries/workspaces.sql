-- name: CreateWorkspace :one
INSERT INTO workspaces (slug, name) VALUES (@slug, @name) RETURNING *;

-- name: GetWorkspace :one
SELECT * FROM workspaces WHERE id = @id;

-- name: GetMembership :one
SELECT w.id, w.slug, w.name, w.public, m.role
FROM workspaces w
JOIN memberships m ON m.workspace_id = w.id AND m.account_id = @account_id
WHERE w.slug = @slug;

-- name: ListMyWorkspaces :many
SELECT w.id, w.slug, w.name, m.role,
       (SELECT count(*) FROM memberships x WHERE x.workspace_id = w.id) AS members
FROM memberships m
JOIN workspaces w ON w.id = m.workspace_id
WHERE m.account_id = @account_id
ORDER BY lower(w.name), w.id;

-- name: UpdateWorkspace :execrows
UPDATE workspaces SET name = @name, slug = @slug, public = @public WHERE id = @id;

-- name: AddMember :exec
INSERT INTO memberships (workspace_id, account_id, role)
VALUES (@workspace_id, @account_id, @role)
ON CONFLICT (workspace_id, account_id) DO NOTHING;

-- name: ListMembers :many
SELECT a.id, a.email, a.name, m.role, m.created_at
FROM memberships m
JOIN accounts a ON a.id = m.account_id
WHERE m.workspace_id = @workspace_id
ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'member' THEN 2 ELSE 3 END,
         lower(a.name), a.id;

-- name: GetMember :one
SELECT a.id, a.email, a.name, m.role, m.created_at
FROM memberships m
JOIN accounts a ON a.id = m.account_id
WHERE m.workspace_id = @workspace_id AND m.account_id = @account_id;

-- name: LockOwners :many
SELECT account_id FROM memberships
WHERE workspace_id = @workspace_id AND role = 'owner'
FOR UPDATE;

-- name: UpdateMemberRole :execrows
UPDATE memberships SET role = @role WHERE workspace_id = @workspace_id AND account_id = @account_id;

-- name: DeleteMember :execrows
DELETE FROM memberships WHERE workspace_id = @workspace_id AND account_id = @account_id;

-- name: UpsertInvitation :one
INSERT INTO invitations (workspace_id, email, role, token_hash, invited_by, expires_at)
VALUES (@workspace_id, @email, @role, @token_hash, @invited_by, @expires_at)
ON CONFLICT (workspace_id, email) DO UPDATE
SET role = excluded.role, token_hash = excluded.token_hash, invited_by = excluded.invited_by,
    created_at = now(), expires_at = excluded.expires_at
RETURNING id;

-- name: ListInvitations :many
SELECT i.id, i.email, i.role, i.created_at, i.expires_at, a.name AS invited_by_name
FROM invitations i
LEFT JOIN accounts a ON a.id = i.invited_by
WHERE i.workspace_id = @workspace_id AND i.expires_at > now()
ORDER BY i.created_at DESC, i.id DESC;

-- name: DeleteInvitation :execrows
DELETE FROM invitations WHERE workspace_id = @workspace_id AND id = @id;

-- name: GetInvitation :one
SELECT i.workspace_id, i.id, i.email, i.role, w.name AS workspace_name, w.slug AS workspace_slug
FROM invitations i
JOIN workspaces w ON w.id = i.workspace_id
WHERE i.token_hash = @token_hash AND i.expires_at > now();

-- name: RecordAudit :exec
INSERT INTO audit_log (actor_id, workspace_id, action, target)
VALUES (sqlc.narg(actor_id), sqlc.narg(workspace_id), @action, @target);
