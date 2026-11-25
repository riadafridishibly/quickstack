-- name: GetSiteSettings :one
SELECT sqlc.embed(s), i.key AS icon_key, l.content_type AS live_icon_content_type
FROM site_settings s
LEFT JOIN assets i ON i.id = s.icon_id
LEFT JOIN assets l ON l.id = s.live_icon_id;

-- name: LockSiteSettings :one
SELECT * FROM site_settings FOR UPDATE;

-- name: UpdateSiteIcon :one
-- Clearing the icon takes it off the pages at once; a new one replaces the
-- live icon when its job has run.
UPDATE site_settings
SET icon_id = sqlc.narg(icon_id)::bigint,
    icon_background = @icon_background,
    icon_rev = icon_rev + 1,
    live_icon_id = CASE WHEN sqlc.narg(icon_id)::bigint IS NULL THEN NULL ELSE live_icon_id END,
    updated_at = now()
RETURNING *;

-- name: MarkSiteIconLive :execrows
-- The media_icon job calls it once rev's files are written. A job for an
-- icon that is no longer chosen, or that finishes after a later one,
-- changes nothing.
UPDATE site_settings
SET live_icon_id = @icon_id::bigint, live_icon_rev = @rev
WHERE icon_id = @icon_id AND live_icon_rev < @rev;
