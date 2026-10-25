-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_sources DROP CONSTRAINT {{.Prefix}}chk_knowledge_source_kind;
ALTER TABLE {{.Prefix}}knowledge_sources ADD CONSTRAINT {{.Prefix}}chk_knowledge_source_kind CHECK (source_kind IN ('UPLOAD','URL','REPOSITORY','REQUIREMENT','FEEDBACK','DECISION','DELIVERY'));
ALTER TABLE {{.Prefix}}knowledge_sources ADD COLUMN origin_kind VARCHAR(32) NULL;
ALTER TABLE {{.Prefix}}knowledge_sources ADD COLUMN origin_id BIGINT NULL;
ALTER TABLE {{.Prefix}}knowledge_sources ADD COLUMN origin_version INT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}knowledge_sources ADD COLUMN work_item_id BIGINT NULL;
ALTER TABLE {{.Prefix}}knowledge_sources ADD COLUMN client_accepted BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX {{.Prefix}}uq_knowledge_source_origin ON {{.Prefix}}knowledge_sources (tenant_id,origin_kind,origin_id,source_kind);

-- +goose Down
DROP INDEX {{.Prefix}}uq_knowledge_source_origin;
ALTER TABLE {{.Prefix}}knowledge_sources DROP COLUMN client_accepted;
ALTER TABLE {{.Prefix}}knowledge_sources DROP COLUMN work_item_id;
ALTER TABLE {{.Prefix}}knowledge_sources DROP COLUMN origin_version;
ALTER TABLE {{.Prefix}}knowledge_sources DROP COLUMN origin_id;
ALTER TABLE {{.Prefix}}knowledge_sources DROP COLUMN origin_kind;
