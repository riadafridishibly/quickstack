-- name: TaskCounts :one
SELECT count(*) FILTER (WHERE t.status = 'todo') AS todo,
       count(*) FILTER (WHERE t.status = 'doing') AS doing,
       count(*) FILTER (WHERE t.completed_at > now() - interval '7 days') AS done_this_week,
       count(*) FILTER (WHERE t.status <> 'done' AND t.due_on < current_date) AS overdue
FROM tasks t
JOIN projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
WHERE t.workspace_id = @workspace_id AND p.status = 'active';

-- name: DailyTaskFlow :many
WITH days AS (
    SELECT generate_series(@since::date, current_date, interval '1 day')::date AS day
), created AS (
    SELECT tc.created_at::date AS day, count(*) AS n
    FROM tasks tc
    WHERE tc.workspace_id = @workspace_id AND tc.created_at >= @since::date
    GROUP BY 1
), completed AS (
    SELECT td.completed_at::date AS day, count(*) AS n
    FROM tasks td
    WHERE td.workspace_id = @workspace_id AND td.completed_at >= @since::date
    GROUP BY 1
)
SELECT d.day, coalesce(c.n, 0)::bigint AS created, coalesce(x.n, 0)::bigint AS completed
FROM days d
LEFT JOIN created c ON c.day = d.day
LEFT JOIN completed x ON x.day = d.day
ORDER BY d.day;

-- name: OpenTasksByAssignee :many
SELECT coalesce(a.name, '')::text AS name, count(*) AS open
FROM tasks t
JOIN projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
LEFT JOIN accounts a ON a.id = t.assignee_id
WHERE t.workspace_id = @workspace_id AND t.status <> 'done' AND p.status = 'active'
GROUP BY t.assignee_id, a.name
ORDER BY 2 DESC, 1
LIMIT 8;

-- name: MyOpenTasks :many
SELECT t.id, t.title, t.status, t.due_on, p.id AS project_id, p.name AS project_name
FROM tasks t
JOIN projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
WHERE t.workspace_id = @workspace_id AND t.assignee_id = @account_id::bigint AND t.status <> 'done'
  AND p.status = 'active'
ORDER BY t.due_on NULLS LAST, t.id
LIMIT 10;
