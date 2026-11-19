-- name: CreateTOTP :execrows
INSERT INTO totp_credentials (account_id, secret, last_step)
VALUES (@account_id, @secret, @last_step)
ON CONFLICT (account_id) DO NOTHING;

-- name: GetTOTP :one
SELECT * FROM totp_credentials WHERE account_id = @account_id;

-- UseTOTPStep spends a code's step, so the same code can't be used again,
-- even by a request running at the same time.
-- name: UseTOTPStep :execrows
UPDATE totp_credentials SET last_step = @step
WHERE account_id = @account_id AND last_step < @step;

-- name: DeleteTOTP :execrows
DELETE FROM totp_credentials WHERE account_id = @account_id;
