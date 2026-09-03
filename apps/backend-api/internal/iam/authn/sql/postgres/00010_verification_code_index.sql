-- Supports the public verification-code lookup without scanning token history.
-- +goose Up
CREATE INDEX {{.Prefix}}idx_iam_email_verification_code_active
    ON {{.Prefix}}iam_email_verification_tokens (code, consumed_at, expires_at);

-- +goose Down
DROP INDEX {{.Prefix}}idx_iam_email_verification_code_active;
