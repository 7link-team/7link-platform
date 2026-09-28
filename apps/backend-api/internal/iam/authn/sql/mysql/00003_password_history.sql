-- Source: internal/modules/iam/sql/mysql/00004_password_history.sql:2-12
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_password_history (
    id            BIGINT       NOT NULL PRIMARY KEY,
    principal_id  BIGINT       NOT NULL,
    password_hash TEXT         COLLATE utf8mb4_bin NOT NULL,
    created_at    DATETIME(3)  NOT NULL,
    INDEX {{.Prefix}}idx_iam_password_history_principal (principal_id, created_at DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_password_history;
