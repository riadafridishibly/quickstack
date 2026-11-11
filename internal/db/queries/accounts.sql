-- name: CreateAccount :one
INSERT INTO accounts (email, name, password_hash)
VALUES (@email, @name, @password_hash)
RETURNING *;

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = @id;

-- name: GetAccountByEmail :one
SELECT * FROM accounts WHERE email = @email;

-- name: UpdateAccountName :execrows
UPDATE accounts SET name = @name, updated_at = now() WHERE id = @id;

-- name: UpdateAccountPassword :execrows
UPDATE accounts SET password_hash = @password_hash, updated_at = now() WHERE id = @id;

-- name: SetPlatformRole :execrows
UPDATE accounts SET platform_role = sqlc.narg(platform_role), updated_at = now() WHERE id = @id;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, account_id, user_agent, expires_at)
VALUES (@token_hash, @account_id, @user_agent, @expires_at);

-- name: GetSession :one
SELECT s.id AS session_id, s.last_seen_at, a.id, a.email, a.name, a.platform_role
FROM sessions s
JOIN accounts a ON a.id = s.account_id
WHERE s.token_hash = @token_hash AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now(), expires_at = @expires_at WHERE token_hash = @token_hash;

-- name: ListSessions :many
SELECT id, user_agent, created_at, last_seen_at
FROM sessions
WHERE account_id = @account_id AND expires_at > now()
ORDER BY last_seen_at DESC, id DESC;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: DeleteSessionByID :execrows
DELETE FROM sessions WHERE account_id = @account_id AND id = @id;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE account_id = @account_id AND expires_at <= now();

-- name: DeleteAccountSessions :execrows
DELETE FROM sessions WHERE account_id = @account_id;

-- name: DeleteOtherSessions :execrows
DELETE FROM sessions WHERE account_id = @account_id AND token_hash <> @token_hash;

-- name: CreatePasswordReset :exec
INSERT INTO password_resets (token_hash, account_id, expires_at)
VALUES (@token_hash, @account_id, @expires_at);

-- name: GetPasswordReset :one
SELECT account_id FROM password_resets WHERE token_hash = @token_hash AND expires_at > now();

-- name: TakePasswordReset :one
DELETE FROM password_resets
WHERE token_hash = @token_hash AND expires_at > now()
RETURNING account_id;

-- name: DeletePasswordResets :exec
DELETE FROM password_resets WHERE account_id = @account_id;

-- name: PurgeExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();

-- name: PurgeExpiredPasswordResets :execrows
DELETE FROM password_resets WHERE expires_at <= now();

-- name: PurgeExpiredInvitations :execrows
DELETE FROM invitations WHERE expires_at <= now();
