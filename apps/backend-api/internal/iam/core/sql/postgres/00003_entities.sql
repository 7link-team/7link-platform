-- Sources: internal/modules/iam/sql/postgres/00003_local_identity.sql:45-52;
-- internal/modules/iam/sql/postgres/00011_entity_constraints.sql:2-3.
-- Validation: internal/modules/iam/entity.go:367-388,462-471.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_entities (
    tenant_id  BIGINT         NOT NULL,
    id         BIGINT         NOT NULL,
    parent_id  BIGINT         NULL,
    type       VARCHAR(64)    COLLATE "C" NOT NULL,
    name       VARCHAR(200)   NOT NULL,
    name_key   VARCHAR(200)   COLLATE "C" NOT NULL,
    status     VARCHAR(16)    COLLATE "C" NOT NULL DEFAULT 'active',
    metadata   TEXT           NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
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
