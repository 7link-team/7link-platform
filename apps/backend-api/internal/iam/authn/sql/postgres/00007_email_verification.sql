-- Source: internal/modules/iam/sql/postgres/00009_email_verification.sql:2-14
-- Verification code: internal/modules/iam/sql/postgres/00025_email_verification_code.sql:2.
-- Code validation: internal/modules/authn/verification.go:141-185.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_email_verification_tokens (
    token_hmac   CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    principal_id BIGINT         NOT NULL,
    email        VARCHAR(320)   COLLATE "C" NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    expires_at   TIMESTAMPTZ(3) NOT NULL,
    consumed_at  TIMESTAMPTZ(3) NULL,
    code         VARCHAR(6)     COLLATE "C" NOT NULL DEFAULT ''
);

CREATE INDEX {{.Prefix}}idx_iam_email_verification_principal
    ON {{.Prefix}}iam_email_verification_tokens (principal_id, consumed_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_email_verification_tokens;
