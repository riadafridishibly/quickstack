-- name: CreateAsset :one
-- Returns no row if the key is taken; the caller picks another.
INSERT INTO assets (key, content_type, bytes, sha256, width, height, created_by)
VALUES (@key, @content_type, @bytes, @sha256, @width, @height, @created_by)
ON CONFLICT (key) DO NOTHING
RETURNING *;

-- name: GetAsset :one
SELECT * FROM assets WHERE id = @id;

-- name: GetAssetByKey :one
SELECT * FROM assets WHERE key = @key;

-- name: ListAssets :many
SELECT a.*, coalesce(c.name, '') AS created_by_name, count(*) OVER () AS total
FROM assets a
LEFT JOIN accounts c ON c.id = a.created_by
ORDER BY a.created_at DESC, a.id DESC
LIMIT @lim OFFSET @off;

-- name: MarkAssetReady :execrows
UPDATE assets SET status = 'ready', error = '', width = @width, height = @height, widths = @widths
WHERE id = @id;

-- name: MarkAssetFailed :execrows
UPDATE assets SET status = 'failed', error = @error WHERE id = @id;

-- name: UpdateAssetText :execrows
UPDATE assets SET alt = @alt, credit = @credit WHERE key = @key;

-- name: DeleteAsset :one
DELETE FROM assets WHERE key = @key RETURNING id;
