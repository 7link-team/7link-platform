-- Sources: internal/modules/iam/sql/mysql/00016_resource_relationships.sql:2-18;
-- internal/modules/iam/sql/mysql/00017_relationship_windows.sql:5-7;
-- internal/modules/iam/sql/mysql/00018_relationship_conditions.sql:3.
-- Validation: internal/modules/iam/relationship.go:302-353,356-365.
-- MySQL DDL auto-commits; this file owns one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_resource_relationships (
    tenant_id        BIGINT      NOT NULL,
    entity_id        BIGINT      NOT NULL,
    subject_type     VARCHAR(16) COLLATE utf8mb4_bin NOT NULL,
    subject_id       BIGINT      NOT NULL,
    subject_relation VARCHAR(16) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    relation         VARCHAR(16) COLLATE utf8mb4_bin NOT NULL,
    resource_type    VARCHAR(64) COLLATE utf8mb4_bin NOT NULL,
    resource_id      BIGINT      NOT NULL,
    starts_at        DATETIME(3) NULL,
    ends_at          DATETIME(3) NULL,
    condition_json   TEXT        NOT NULL DEFAULT (''),
    created_at       DATETIME(3) NOT NULL,
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
        CHECK (relation IN ('owner', 'editor', 'viewer')),
    INDEX {{.Prefix}}idx_iam_resource_relationships_subject
        (tenant_id, subject_type, subject_id, subject_relation),
    INDEX {{.Prefix}}idx_iam_resource_relationships_target
        (tenant_id, entity_id, resource_type, resource_id, relation)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_resource_relationships;
