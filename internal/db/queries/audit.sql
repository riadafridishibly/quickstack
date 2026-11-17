-- name: RecordAudit :exec
INSERT INTO audit_log (actor_id, workspace_id, action, target)
VALUES (sqlc.narg(actor_id), sqlc.narg(workspace_id), @action, @target);
