-- name: CreatePost :one
INSERT INTO posts (slug, title, summary, body, cover_id, author_id, status, published_at, tags)
VALUES (@slug, @title, @summary, @body, @cover_id, @author_id, @status, @published_at, @tags)
RETURNING *;

-- name: GetPost :one
SELECT sqlc.embed(p), a.name AS author_name, c.key AS cover_key
FROM posts p
LEFT JOIN accounts a ON a.id = p.author_id
LEFT JOIN assets c ON c.id = p.cover_id
WHERE p.id = @id;

-- name: LockPost :one
SELECT * FROM posts WHERE id = @id FOR UPDATE;

-- name: UpdatePost :one
UPDATE posts
SET slug = @slug, title = @title, summary = @summary, body = @body, cover_id = @cover_id,
    status = @status, published_at = @published_at, tags = @tags, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: DeletePost :one
DELETE FROM posts WHERE id = @id RETURNING slug;

-- name: ListPosts :many
-- state is draft, scheduled or published (live); empty lists every post.
SELECT p.id, p.slug, p.title, p.status, p.published_at, p.updated_at,
       a.name AS author_name, count(*) OVER () AS total
FROM posts p
LEFT JOIN accounts a ON a.id = p.author_id
WHERE (@query::text = '' OR p.title ILIKE '%' || @query::text || '%')
  AND CASE @state::text
        WHEN 'draft' THEN p.status = 'draft'
        WHEN 'scheduled' THEN p.status = 'published' AND p.published_at > now()
        WHEN 'published' THEN p.status = 'published' AND p.published_at <= now()
        ELSE true
      END
ORDER BY p.updated_at DESC, p.id DESC
LIMIT @lim OFFSET @off;

-- name: ClearPostMedia :exec
DELETE FROM post_media WHERE post_id = @post_id;

-- name: AddPostMedia :exec
INSERT INTO post_media (post_id, asset_id)
SELECT @post_id, unnest(@asset_ids::bigint[])
ON CONFLICT DO NOTHING;

-- name: AddPostRedirect :exec
INSERT INTO post_redirects (old_slug, post_id) VALUES (@old_slug, @post_id)
ON CONFLICT (old_slug) DO UPDATE SET post_id = excluded.post_id;

-- name: DeletePostRedirect :exec
DELETE FROM post_redirects WHERE old_slug = @old_slug;

-- name: GetLivePost :one
SELECT sqlc.embed(p), a.name AS author_name
FROM posts p
LEFT JOIN accounts a ON a.id = p.author_id
WHERE p.slug = @slug AND p.status = 'published' AND p.published_at <= now();

-- name: GetRedirectedSlug :one
-- The current slug of the live post that once had old_slug.
SELECT p.slug
FROM post_redirects r
JOIN posts p ON p.id = r.post_id
WHERE r.old_slug = @old_slug AND p.status = 'published' AND p.published_at <= now();

-- name: ListLivePosts :many
-- An empty tag lists every live post.
SELECT p.id, p.slug, p.title, p.summary, p.published_at::timestamptz AS published_at, p.tags,
       count(*) OVER () AS total
FROM posts p
WHERE p.status = 'published' AND p.published_at <= now()
  AND (@tag::text = '' OR @tag::text = ANY (p.tags))
ORDER BY p.published_at DESC, p.id DESC
LIMIT @lim OFFSET @off;

-- name: ListRecentPosts :many
SELECT sqlc.embed(p), a.name AS author_name
FROM posts p
LEFT JOIN accounts a ON a.id = p.author_id
WHERE p.status = 'published' AND p.published_at <= now()
ORDER BY p.published_at DESC, p.id DESC
LIMIT @lim;

-- name: ListLivePostDates :many
SELECT p.slug, greatest(p.updated_at, p.published_at)::timestamptz AS modified
FROM posts p
WHERE p.status = 'published' AND p.published_at <= now()
ORDER BY p.published_at DESC, p.id DESC;
