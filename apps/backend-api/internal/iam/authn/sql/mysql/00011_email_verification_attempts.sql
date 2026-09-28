-- Adds a durable per-credential limit for the six-digit verification flow.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_email_verification_tokens
    ADD COLUMN attempts INT NOT NULL DEFAULT 0;

ALTER TABLE {{.Prefix}}iam_email_verification_tokens
    ADD CONSTRAINT {{.Prefix}}chk_iam_email_verification_attempts
        CHECK (attempts >= 0 AND attempts <= 5);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_email_verification_tokens
    DROP CHECK {{.Prefix}}chk_iam_email_verification_attempts;

ALTER TABLE {{.Prefix}}iam_email_verification_tokens
    DROP COLUMN attempts;
