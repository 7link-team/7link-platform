-- Source: internal/modules/iam/sql/mysql/00003_local_identity.sql:49-55.
-- Validation: internal/modules/iam/entity.go:425-440.
-- MySQL DDL auto-commits; this file owns one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_role_bindings (
    tenant_id    BIGINT      NOT NULL,
    role_id      BIGINT      NOT NULL,
    principal_id BIGINT      NOT NULL,
    scope_type   VARCHAR(32) COLLATE utf8mb4_bin NOT NULL DEFAULT 'tenant',
    scope_id     BIGINT      NOT NULL,
    effect       VARCHAR(8)  COLLATE utf8mb4_bin NOT NULL DEFAULT 'allow',
    expires_at   DATETIME(3) NULL,
    created_at   DATETIME(3) NOT NULL,
    PRIMARY KEY (tenant_id, role_id, principal_id, scope_type, scope_id),
    CONSTRAINT {{.Prefix}}fk_iam_role_bindings_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_role_bindings_scope
        CHECK (scope_type IN ('tenant', 'entity')),
    CONSTRAINT {{.Prefix}}chk_iam_role_bindings_effect
        CHECK (effect IN ('allow', 'deny')),
    INDEX {{.Prefix}}idx_iam_role_bindings_subject
        (tenant_id, principal_id, scope_type, scope_id, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_role_bindings;
