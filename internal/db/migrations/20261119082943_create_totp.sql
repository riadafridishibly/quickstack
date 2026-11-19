-- +goose Up
-- secret is sealed with QUICKSTACK_SECRET_KEY. last_step is the 30-second
-- step of the last code accepted; a code is accepted only for a later step,
-- so each code works once.
CREATE TABLE totp_credentials (
    account_id bigint      PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    secret     bytea       NOT NULL,
    last_step  bigint      NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE totp_credentials;
