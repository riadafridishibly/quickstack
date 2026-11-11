-- name: PlatformCounts :one
SELECT (SELECT count(*) FROM accounts) AS accounts,
       (SELECT count(*) FROM workspaces) AS workspaces,
       (SELECT count(DISTINCT account_id) FROM sessions
        WHERE expires_at > now() AND last_seen_at > now() - interval '7 days') AS active_accounts;

-- name: DailySignups :many
WITH days AS (
    SELECT generate_series(@since::date, current_date, interval '1 day')::date AS day
), signups AS (
    SELECT created_at::date AS day, count(*) AS n FROM accounts WHERE created_at >= @since::date GROUP BY 1
)
SELECT d.day, coalesce(s.n, 0)::bigint AS accounts
FROM days d
LEFT JOIN signups s ON s.day = d.day
ORDER BY d.day;

-- name: ListAccounts :many
SELECT a.id, a.email, a.name, a.platform_role, a.created_at,
       (SELECT count(*) FROM memberships m WHERE m.account_id = a.id) AS workspaces,
       count(*) OVER () AS total
FROM accounts a
WHERE (@query::text = '' OR a.email ILIKE '%' || @query::text || '%' OR a.name ILIKE '%' || @query::text || '%')
  AND (NOT @staff_only::boolean OR a.platform_role IS NOT NULL)
ORDER BY a.created_at DESC, a.id DESC
LIMIT @lim OFFSET @off;

-- name: ListAccountMemberships :many
SELECT w.id, w.slug, w.name, m.role, m.created_at
FROM memberships m
JOIN workspaces w ON w.id = m.workspace_id
WHERE m.account_id = @account_id
ORDER BY lower(w.name), w.id;

-- name: ListWorkspaces :many
SELECT w.id, w.slug, w.name, w.public, w.created_at,
       (SELECT count(*) FROM memberships m WHERE m.workspace_id = w.id) AS members,
       count(*) OVER () AS total
FROM workspaces w
WHERE (@query::text = '' OR w.name ILIKE '%' || @query::text || '%' OR w.slug ILIKE '%' || @query::text || '%')
ORDER BY w.created_at DESC, w.id DESC
LIMIT @lim OFFSET @off;

-- name: ListAudit :many
SELECT l.id, l.at, l.action, l.target, a.email AS actor_email, w.slug AS workspace_slug,
       count(*) OVER () AS total
FROM audit_log l
LEFT JOIN accounts a ON a.id = l.actor_id
LEFT JOIN workspaces w ON w.id = l.workspace_id
WHERE (@action::text = '' OR l.action = @action::text)
ORDER BY l.at DESC, l.id DESC
LIMIT @lim OFFSET @off;

-- name: ListAuditActions :many
SELECT DISTINCT action FROM audit_log ORDER BY action;
