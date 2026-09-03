-- Source: internal/modules/iam/sql/mysql/00009_email_verification.sql:2-14
-- Verification code: internal/modules/iam/sql/mysql/00025_email_verification_code.sql:2.
-- Code validation: internal/modules/authn/verification.go:141-185.
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_email_verification_tokens (
    token_hmac   CHAR(64)      COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    principal_id BIGINT        NOT NULL,
    email        VARCHAR(320)  COLLATE utf8mb4_bin NOT NULL,
    created_at   DATETIME(3)   NOT NULL,
    expires_at   DATETIME(3)   NOT NULL,
    consumed_at  DATETIME(3)   NULL,
    code         VARCHAR(6)    COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    INDEX {{.Prefix}}idx_iam_email_verification_principal (principal_id, consumed_at, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_email_verification_tokens;
