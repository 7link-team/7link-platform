-- Sources: internal/modules/iam/sql/postgres/00015_relationships.sql:2-15;
-- internal/modules/iam/sql/postgres/00017_relationship_windows.sql:2-4;
-- internal/modules/iam/sql/postgres/00018_relationship_conditions.sql:2.
-- Validation: internal/modules/iam/relationship.go:302-353,356-366.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_relationships (
    tenant_id       BIGINT         NOT NULL,
    subject_type    VARCHAR(16)    COLLATE "C" NOT NULL,
    subject_id      BIGINT         NOT NULL,
    subject_relation VARCHAR(16)   COLLATE "C" NOT NULL DEFAULT '',
    relation        VARCHAR(16)    COLLATE "C" NOT NULL,
    resource_type   VARCHAR(64)    COLLATE "C" NOT NULL,
    resource_id     BIGINT         NOT NULL,
    starts_at       TIMESTAMPTZ(3) NULL,
    ends_at         TIMESTAMPTZ(3) NULL,
    condition_json  TEXT           NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (
        tenant_id, subject_type, subject_id, subject_relation,
        relation, resource_type, resource_id
    ),
    CONSTRAINT {{.Prefix}}fk_iam_relationships_resource
        FOREIGN KEY (tenant_id, resource_id, resource_type)
        REFERENCES {{.Prefix}}iam_entities (tenant_id, id, type) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_relationships_subject_type
        CHECK (subject_type IN ('principal', 'group', 'position', 'entity')),
    CONSTRAINT {{.Prefix}}chk_iam_relationships_relation
        CHECK (relation IN ('owner', 'editor', 'viewer'))
);
CREATE INDEX {{.Prefix}}idx_iam_relationships_subject
    ON {{.Prefix}}iam_relationships
        (tenant_id, subject_type, subject_id, subject_relation);
CREATE INDEX {{.Prefix}}idx_iam_relationships_resource
    ON {{.Prefix}}iam_relationships
        (tenant_id, resource_type, resource_id, relation);

-- +goose Down
DROP TABLE {{.Prefix}}iam_relationships;
