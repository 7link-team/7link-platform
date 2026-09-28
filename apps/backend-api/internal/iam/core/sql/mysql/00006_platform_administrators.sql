-- Source: internal/modules/iam/sql/mysql/00012_platform_administrators.sql:2-5.
-- MySQL DDL auto-commits; this file owns one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_platform_administrators (
    principal_id BIGINT      NOT NULL PRIMARY KEY,
    created_at   DATETIME(3) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_platform_administrators;
