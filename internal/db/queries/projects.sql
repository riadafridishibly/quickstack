-- name: ListProjects :many
SELECT p.id, p.name, p.status, p.public, p.updated_at,
       count(t.id) FILTER (WHERE t.status <> 'done') AS open_tasks,
       count(t.id) AS all_tasks,
       count(*) OVER () AS total
FROM projects p
LEFT JOIN tasks t ON t.workspace_id = p.workspace_id AND t.project_id = p.id
WHERE p.workspace_id = @workspace_id
  AND (@status::text = '' OR p.status = @status::text)
  AND (@query::text = '' OR p.name ILIKE '%' || @query::text || '%')
GROUP BY p.workspace_id, p.id
ORDER BY p.updated_at DESC, p.id DESC
LIMIT @lim OFFSET @off;

-- name: GetProject :one
SELECT * FROM projects WHERE workspace_id = @workspace_id AND id = @id;

-- name: CreateProject :one
INSERT INTO projects (workspace_id, name, description, status, public)
VALUES (@workspace_id, @name, @description, @status, @public)
RETURNING id;

-- name: UpdateProject :execrows
UPDATE projects
SET name = @name, description = @description, status = @status, public = @public, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: TouchProject :exec
UPDATE projects SET updated_at = now() WHERE workspace_id = @workspace_id AND id = @id;

-- name: DeleteProject :execrows
DELETE FROM projects WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListTasks :many
SELECT t.id, t.title, t.status, t.position, t.due_on, t.assignee_id, a.name AS assignee_name
FROM tasks t
LEFT JOIN accounts a ON a.id = t.assignee_id
WHERE t.workspace_id = @workspace_id AND t.project_id = @project_id
ORDER BY CASE t.status WHEN 'todo' THEN 0 WHEN 'doing' THEN 1 ELSE 2 END, t.position, t.id;

-- name: GetTask :one
SELECT * FROM tasks WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: CreateTask :one
INSERT INTO tasks (workspace_id, project_id, title, assignee_id, due_on, position)
VALUES (@workspace_id, @project_id, @title, sqlc.narg(assignee_id), sqlc.narg(due_on),
        (SELECT coalesce(max(x.position), 0) + 1 FROM tasks x
         WHERE x.workspace_id = @workspace_id AND x.project_id = @project_id AND x.status = 'todo'))
RETURNING id;

-- name: UpdateTask :execrows
UPDATE tasks
SET title = @title, assignee_id = sqlc.narg(assignee_id), due_on = sqlc.narg(due_on), updated_at = now()
WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: MoveTask :execrows
-- Moving to another column puts the task at its end; ReorderTasks then sets
-- the order the board shows.
UPDATE tasks t
SET status = @status::text,
    completed_at = CASE WHEN @status::text = 'done' THEN coalesce(t.completed_at, now()) END,
    position = CASE WHEN t.status = @status::text THEN t.position ELSE
        (SELECT coalesce(max(x.position), 0) + 1 FROM tasks x
         WHERE x.workspace_id = t.workspace_id AND x.project_id = t.project_id AND x.status = @status::text) END,
    updated_at = now()
WHERE t.workspace_id = @workspace_id AND t.project_id = @project_id AND t.id = @id;

-- name: ReorderTasks :execrows
UPDATE tasks SET position = array_position(@ids::bigint[], id)
WHERE workspace_id = @workspace_id AND project_id = @project_id
  AND status = @status AND id = ANY(@ids::bigint[]);

-- name: DeleteTask :execrows
DELETE FROM tasks WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;
