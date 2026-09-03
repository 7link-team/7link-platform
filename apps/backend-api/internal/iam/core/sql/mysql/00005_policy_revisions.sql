-- Source: internal/modules/iam/sql/mysql/00010_policy_revision.sql:2-6.
-- MySQL DDL auto-commits; this file owns one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_policy_revisions (
    tenant_id  BIGINT      NOT NULL PRIMARY KEY,
    revision   BIGINT      NOT NULL DEFAULT 0,
    updated_at DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_policy_revisions_revision CHECK (revision >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_policy_revisions;
