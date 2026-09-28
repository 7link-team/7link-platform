-- Sources: internal/modules/iam/sql/sqlite/00016_resource_relationships.sql:2-16;
-- internal/modules/iam/sql/sqlite/00017_relationship_windows.sql:4-5;
-- internal/modules/iam/sql/sqlite/00018_relationship_conditions.sql:3.
-- Validation: internal/modules/iam/relationship.go:302-353,356-365.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_resource_relationships (
    tenant_id        BIGINT   NOT NULL,
    entity_id        BIGINT   NOT NULL,
    subject_type     TEXT     COLLATE BINARY NOT NULL,
    subject_id       BIGINT   NOT NULL,
    subject_relation TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    relation         TEXT     COLLATE BINARY NOT NULL,
    resource_type    TEXT     COLLATE BINARY NOT NULL,
    resource_id      BIGINT   NOT NULL,
    starts_at        DATETIME NULL,
    ends_at          DATETIME NULL,
    condition_json   TEXT     NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL,
    PRIMARY KEY (
        tenant_id, entity_id, subject_type, subject_id, subject_relation,
        relation, resource_type, resource_id
    ),
    CONSTRAINT {{.Prefix}}fk_iam_resource_relationships_entity
        FOREIGN KEY (tenant_id, entity_id)
        REFERENCES {{.Prefix}}iam_entities (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_resource_rels_subject_type
        CHECK (subject_type IN ('principal', 'group', 'position', 'entity')),
    CONSTRAINT {{.Prefix}}chk_iam_resource_relationships_relation
        CHECK (relation IN ('owner', 'editor', 'viewer'))
);
CREATE INDEX {{.Prefix}}idx_iam_resource_relationships_subject
    ON {{.Prefix}}iam_resource_relationships
        (tenant_id, subject_type, subject_id, subject_relation);
CREATE INDEX {{.Prefix}}idx_iam_resource_relationships_target
    ON {{.Prefix}}iam_resource_relationships
        (tenant_id, entity_id, resource_type, resource_id, relation);

-- +goose Down
DROP TABLE {{.Prefix}}iam_resource_relationships;
