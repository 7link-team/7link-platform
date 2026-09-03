-- Source: internal/modules/iam/sql/mysql/00024_platform_grants.sql:2-8.
-- Validation: internal/modules/iam/authorizer.go:88-90.
-- MySQL DDL auto-commits; this file owns one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_platform_grants (
    principal_id    BIGINT       NOT NULL,
    permission_code VARCHAR(128) COLLATE utf8mb4_bin NOT NULL,
    created_at      DATETIME(3)  NOT NULL,
    PRIMARY KEY (principal_id, permission_code),
    INDEX {{.Prefix}}idx_iam_platform_grants_permission
        (permission_code, principal_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_platform_grants;
