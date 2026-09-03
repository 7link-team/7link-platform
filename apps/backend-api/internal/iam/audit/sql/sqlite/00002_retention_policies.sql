-- Source: internal/modules/iam/sql/sqlite/00022_audit_retention_policies.sql:2-6.
-- Validation: internal/modules/audit/governance.go:15-18,72-77.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_audit_retention_policies (
    tenant_id          BIGINT   NOT NULL PRIMARY KEY,
    min_days           INTEGER  NOT NULL DEFAULT 365,
    archive_after_days INTEGER  NOT NULL DEFAULT 730,
    updated_at         DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_audit_retention_min_days
        CHECK (min_days BETWEEN 1 AND 36500),
    CONSTRAINT {{.Prefix}}chk_iam_audit_retention_archive_days
        CHECK (archive_after_days BETWEEN min_days AND 36500)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_audit_retention_policies;
