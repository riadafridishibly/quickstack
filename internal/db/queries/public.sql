-- name: GetPublicWorkspace :one
SELECT id, slug, name FROM workspaces WHERE slug = @slug AND public;

-- name: ListRoadmap :many
-- Done tasks stay on the roadmap for 30 days.
SELECT p.id AS project_id, p.name AS project_name, p.description, t.id, t.title, t.status, t.completed_at
FROM projects p
JOIN tasks t ON t.workspace_id = p.workspace_id AND t.project_id = p.id
WHERE p.workspace_id = @workspace_id AND p.public AND p.status = 'active'
  AND (t.status <> 'done' OR t.completed_at > now() - interval '30 days')
ORDER BY lower(p.name), p.id, CASE t.status WHEN 'doing' THEN 0 WHEN 'todo' THEN 1 ELSE 2 END, t.position, t.id;
