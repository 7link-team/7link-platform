-- Sources: internal/modules/iam/sql/sqlite/00003_local_identity.sql:98-112;
-- internal/modules/iam/sql/sqlite/00011_entity_constraints.sql:2-3.
-- Validation: internal/modules/iam/entity.go:367-388,462-471.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_entities (
    tenant_id  BIGINT   NOT NULL,
    id         BIGINT   NOT NULL,
    parent_id  BIGINT   NULL,
    type       TEXT     COLLATE BINARY NOT NULL,
    name       TEXT     NOT NULL,
    name_key   TEXT     COLLATE BINARY NOT NULL,
    status     TEXT     COLLATE BINARY NOT NULL DEFAULT 'active',
    metadata   TEXT     NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_entities_id_type UNIQUE (tenant_id, id, type),
    CONSTRAINT {{.Prefix}}fk_iam_entities_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_entities (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_entities_status
        CHECK (status IN ('active', 'disabled'))
);
CREATE UNIQUE INDEX {{.Prefix}}uq_iam_entities_root_name
    ON {{.Prefix}}iam_entities (tenant_id, type, name_key)
    WHERE parent_id IS NULL;
CREATE UNIQUE INDEX {{.Prefix}}uq_iam_entities_child_name
    ON {{.Prefix}}iam_entities (tenant_id, parent_id, type, name_key)
    WHERE parent_id IS NOT NULL;
CREATE INDEX {{.Prefix}}idx_iam_entities_tree
    ON {{.Prefix}}iam_entities (tenant_id, parent_id, type, status);

-- +goose Down
DROP TABLE {{.Prefix}}iam_entities;
