-- Source: internal/modules/iam/sql/sqlite/00009_email_verification.sql:2-15
-- Verification code: internal/modules/iam/sql/sqlite/00025_email_verification_code.sql:2.
-- Code validation: internal/modules/authn/verification.go:141-185.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_email_verification_tokens (
    token_hmac   TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT   NOT NULL,
    email        TEXT     COLLATE BINARY NOT NULL,
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,
    consumed_at  DATETIME NULL,
    code         TEXT     COLLATE BINARY NOT NULL DEFAULT ''
);

CREATE INDEX {{.Prefix}}idx_iam_email_verification_principal
    ON {{.Prefix}}iam_email_verification_tokens (principal_id, consumed_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_email_verification_tokens;
