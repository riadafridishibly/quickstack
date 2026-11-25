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
    icon_error = '',
    live_icon_id = CASE WHEN sqlc.narg(icon_id)::bigint IS NULL THEN NULL ELSE live_icon_id END,
    updated_at = now()
RETURNING *;

-- name: MarkSiteIconLive :one
-- The media_icon job calls it once rev's files are written, and gets the
-- rev it replaced, 0 for none. A job for an icon that is no longer chosen,
-- or that finishes after a later one, changes nothing and gets no row.
WITH prev AS (SELECT live_icon_rev FROM site_settings FOR UPDATE)
UPDATE site_settings
SET live_icon_id = @icon_id::bigint, live_icon_rev = @rev::integer
FROM prev
WHERE site_settings.icon_id = @icon_id AND site_settings.live_icon_rev < @rev
RETURNING prev.live_icon_rev;

-- name: MarkSiteIconFailed :exec
-- The media_icon job calls it when it gives up on rev. A failure of a rev
-- that is no longer the latest isn't shown.
UPDATE site_settings SET icon_error = @error WHERE icon_rev = @rev;

-- name: LockLiveIconOf :one
-- The live icon's rev, if the asset with key is the live icon.
SELECT s.live_icon_rev
FROM site_settings s
JOIN assets a ON a.id = s.live_icon_id
WHERE a.key = @key
FOR UPDATE OF s;
