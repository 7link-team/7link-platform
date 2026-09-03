-- Source: internal/modules/iam/sql/sqlite/00003_local_identity.sql:114-126.
-- Validation: internal/modules/iam/entity.go:425-440.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_role_bindings (
    tenant_id    BIGINT   NOT NULL,
    role_id      BIGINT   NOT NULL,
    principal_id BIGINT   NOT NULL,
    scope_type   TEXT     COLLATE BINARY NOT NULL DEFAULT 'tenant',
    scope_id     BIGINT   NOT NULL,
    effect       TEXT     COLLATE BINARY NOT NULL DEFAULT 'allow',
    expires_at   DATETIME NULL,
    created_at   DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id, principal_id, scope_type, scope_id),
    CONSTRAINT {{.Prefix}}fk_iam_role_bindings_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_role_bindings_scope
        CHECK (scope_type IN ('tenant', 'entity')),
    CONSTRAINT {{.Prefix}}chk_iam_role_bindings_effect
        CHECK (effect IN ('allow', 'deny'))
);
CREATE INDEX {{.Prefix}}idx_iam_role_bindings_subject
    ON {{.Prefix}}iam_role_bindings
        (tenant_id, principal_id, scope_type, scope_id, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_role_bindings;
