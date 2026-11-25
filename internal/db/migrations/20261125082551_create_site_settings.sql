-- +goose Up
-- Settings for the whole site, in one row. The icon's files are drawn by the
-- media_icon job into site/icon/{rev}/ in the blob store; icon_rev goes up
-- with each change. Pages link the files of live_icon_rev, drawn from
-- live_icon_id, so they keep the previous icon until the job has run.
-- Deleting an asset clears both references to it. icon_error says why the
-- job gave up on icon_rev; each change clears it.
CREATE TABLE site_settings (
    id              boolean     PRIMARY KEY DEFAULT true CHECK (id),
    icon_id         bigint      REFERENCES assets (id) ON DELETE SET NULL,
    icon_background text        NOT NULL DEFAULT '#ffffff' CHECK (icon_background ~ '^#[0-9a-f]{6}$'),
    icon_rev        integer     NOT NULL DEFAULT 0,
    live_icon_id    bigint      REFERENCES assets (id) ON DELETE SET NULL,
    live_icon_rev   integer     NOT NULL DEFAULT 0,
    icon_error      text        NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);
INSERT INTO site_settings DEFAULT VALUES;

-- Each server keeps the settings in memory and listens on site_settings
-- for changes, including those made by deleting an asset.
-- +goose StatementBegin
CREATE FUNCTION notify_site_settings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('site_settings', '');
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER site_settings_changed AFTER UPDATE ON site_settings
    FOR EACH ROW EXECUTE FUNCTION notify_site_settings();

-- +goose Down
DROP TABLE site_settings;
DROP FUNCTION notify_site_settings();
