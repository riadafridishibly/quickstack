-- +goose Up
-- Settings for the whole site, in one row. The icon's files are drawn by the
-- media_icon job into site/icon/{rev}/ in the blob store; icon_rev goes up
-- with each change. Pages link the files of live_icon_rev, drawn from
-- live_icon_id, so they keep the previous icon until the job has run.
-- Deleting an asset clears both references to it.
CREATE TABLE site_settings (
    id              boolean     PRIMARY KEY DEFAULT true CHECK (id),
    icon_id         bigint      REFERENCES assets (id) ON DELETE SET NULL,
    icon_background text        NOT NULL DEFAULT '#ffffff' CHECK (icon_background ~ '^#[0-9a-f]{6}$'),
    icon_rev        integer     NOT NULL DEFAULT 0,
    live_icon_id    bigint      REFERENCES assets (id) ON DELETE SET NULL,
    live_icon_rev   integer     NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now()
);
INSERT INTO site_settings DEFAULT VALUES;

-- +goose Down
DROP TABLE site_settings;
