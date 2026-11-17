-- +goose Up
-- The site's blog. Platform tables: posts belong to the site, not to a
-- workspace. A post is live when it is published and published_at has
-- passed, so a future published_at schedules it.
CREATE TABLE posts (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug         text        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 80),
    title        text        NOT NULL,
    summary      text        NOT NULL DEFAULT '',
    body         text        NOT NULL DEFAULT '',
    cover_id     bigint      REFERENCES assets (id) ON DELETE SET NULL,
    author_id    bigint      REFERENCES accounts (id) ON DELETE SET NULL,
    status       text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    published_at timestamptz,
    tags         text[]      NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (status = 'draft' OR published_at IS NOT NULL)
);
CREATE INDEX posts_published ON posts (published_at DESC, id DESC) WHERE status = 'published';
CREATE INDEX posts_updated ON posts (updated_at DESC, id DESC);
CREATE INDEX posts_tags ON posts USING gin (tags) WHERE status = 'published';
CREATE INDEX posts_cover ON posts (cover_id);
CREATE INDEX posts_author ON posts (author_id);

-- The assets a post's body shows, rebuilt on each save.
CREATE TABLE post_media (
    post_id  bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    asset_id bigint NOT NULL REFERENCES assets (id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, asset_id)
);
CREATE INDEX post_media_asset ON post_media (asset_id);

-- Slugs a published post had before, so their links keep working.
CREATE TABLE post_redirects (
    old_slug text   PRIMARY KEY,
    post_id  bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE
);
CREATE INDEX post_redirects_post ON post_redirects (post_id);

-- +goose Down
DROP TABLE post_redirects;
DROP TABLE post_media;
DROP TABLE posts;
