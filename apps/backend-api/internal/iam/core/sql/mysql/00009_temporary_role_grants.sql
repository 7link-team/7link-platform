-- Source: internal/modules/iam/sql/mysql/00020_temporary_role_grants.sql:2-18.
-- Validation: internal/modules/iam/temporary_grant.go:29-44.
-- MySQL DDL auto-commits; this file owns one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_temporary_role_grants (
    tenant_id    BIGINT      NOT NULL,
    id           BIGINT      NOT NULL,
    role_id      BIGINT      NOT NULL,
    principal_id BIGINT      NOT NULL,
    source_type  VARCHAR(32) COLLATE utf8mb4_bin NOT NULL,
    source_id    BIGINT      NOT NULL,
    starts_at    DATETIME(3) NOT NULL,
    ends_at      DATETIME(3) NOT NULL,
    created_by   BIGINT      NOT NULL,
    created_at   DATETIME(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_temporary_role_grants_source
        UNIQUE (tenant_id, source_type, source_id),
    CONSTRAINT {{.Prefix}}fk_iam_temporary_role_grants_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_temporary_role_grants_source
        CHECK (source_type IN ('access_request')),
    INDEX {{.Prefix}}idx_iam_temporary_role_grants_principal
        (tenant_id, principal_id, starts_at, ends_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_temporary_role_grants;
