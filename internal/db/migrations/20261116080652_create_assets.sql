-- +goose Up
-- Uploaded images. A platform table: the blog's images belong to the site,
-- not to a workspace. URLs and Markdown name an asset by its key, which is
-- 8 characters of lowercase Crockford base32. Files live in the blob store
-- under {key}/: the original, a WebP per width in widths, and og.jpg.
CREATE TABLE assets (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key          text        NOT NULL UNIQUE CHECK (key ~ '^[0-9a-hjkmnp-tv-z]{8}$'),
    status       text        NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'ready', 'failed')),
    error        text        NOT NULL DEFAULT '',
    content_type text        NOT NULL,
    bytes        bigint      NOT NULL,
    sha256       bytea       NOT NULL,
    width        integer     NOT NULL,
    height       integer     NOT NULL,
    widths       integer[]   NOT NULL DEFAULT '{}',
    alt          text        NOT NULL DEFAULT '',
    credit       text        NOT NULL DEFAULT '',
    created_by   bigint      REFERENCES accounts (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX assets_created ON assets (created_at DESC, id DESC);

-- +goose Down
DROP TABLE assets;
