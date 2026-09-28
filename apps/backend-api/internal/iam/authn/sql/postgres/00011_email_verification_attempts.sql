-- Adds a durable per-credential limit for the six-digit verification flow.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_email_verification_tokens
    ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0,
    ADD CONSTRAINT {{.Prefix}}chk_iam_email_verification_attempts
        CHECK (attempts >= 0 AND attempts <= 5);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_email_verification_tokens
    DROP CONSTRAINT {{.Prefix}}chk_iam_email_verification_attempts,
    DROP COLUMN attempts;
