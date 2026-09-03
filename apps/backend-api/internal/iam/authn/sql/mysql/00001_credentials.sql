-- Source: internal/modules/iam/sql/mysql/00003_local_identity.sql:9-14
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- Later source columns are folded into this fresh-database baseline.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_credentials (
    principal_id            BIGINT       NOT NULL PRIMARY KEY,
    password_hash           TEXT         COLLATE utf8mb4_bin NOT NULL,
    totp_secret             TEXT         COLLATE utf8mb4_bin NOT NULL DEFAULT (''),
    mfa_required            BOOLEAN      NOT NULL DEFAULT FALSE,
    failed_attempts         INT          NOT NULL DEFAULT 0,
    locked_until            DATETIME(3)  NULL,
    password_changed_at     DATETIME(3)  NOT NULL,
    -- Source: internal/modules/iam/sql/mysql/00005_mfa.sql:2.
    totp_last_used_step     BIGINT       NOT NULL DEFAULT 0,
    -- Source: internal/modules/iam/sql/mysql/00008_password_recovery.sql:2-4.
    credential_version      BIGINT       NOT NULL DEFAULT 1,
    recovery_cooldown_until DATETIME(3)  NULL,
    updated_at              DATETIME(3)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_credentials;
