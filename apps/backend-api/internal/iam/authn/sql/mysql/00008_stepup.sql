-- Source: internal/modules/iam/sql/mysql/00023_stepup_challenges.sql:2-15
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_stepup_challenges (
    id_hash      CHAR(64)     COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    principal_id BIGINT       NOT NULL,
    session_hash CHAR(64)     COLLATE utf8mb4_bin NOT NULL,
    created_at   DATETIME(3)  NOT NULL,
    expires_at   DATETIME(3)  NOT NULL,
    consumed_at  DATETIME(3)  NULL,
    attempts     INT          NOT NULL DEFAULT 0,
    INDEX {{.Prefix}}idx_iam_stepup_challenges_principal (principal_id, consumed_at, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_stepup_challenges;
