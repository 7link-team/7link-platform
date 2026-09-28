-- Sources: internal/modules/iam/sql/mysql/00003_local_identity.sql:42-47;
-- internal/modules/iam/sql/mysql/00011_entity_constraints.sql:2-4.
-- Validation: internal/modules/iam/entity.go:367-388,462-471.
-- MySQL DDL auto-commits; this file owns one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_entities (
    tenant_id BIGINT       NOT NULL,
    id        BIGINT       NOT NULL,
    parent_id BIGINT       NULL,
    parent_key BIGINT GENERATED ALWAYS AS (COALESCE(parent_id, 0)) STORED,
    type      VARCHAR(64)  COLLATE utf8mb4_bin NOT NULL,
    name      VARCHAR(200) NOT NULL,
    name_key  VARCHAR(200) COLLATE utf8mb4_bin NOT NULL,
    status    VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL DEFAULT 'active',
    metadata  TEXT         NOT NULL DEFAULT ('{}'),
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_entities_id_type UNIQUE (tenant_id, id, type),
    CONSTRAINT {{.Prefix}}uq_iam_entities_sibling_name
        UNIQUE (tenant_id, parent_key, type, name_key),
    CONSTRAINT {{.Prefix}}fk_iam_entities_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_entities (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_entities_id CHECK (id > 0),
    CONSTRAINT {{.Prefix}}chk_iam_entities_status
        CHECK (status IN ('active', 'disabled')),
    INDEX {{.Prefix}}idx_iam_entities_tree (tenant_id, parent_id, type, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_entities;
