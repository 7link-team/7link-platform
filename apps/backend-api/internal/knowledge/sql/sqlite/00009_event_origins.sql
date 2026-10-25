-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_sources_event (
 tags_json VARCHAR(2048) NOT NULL DEFAULT '[]', archived_at DATETIME NULL,
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, scope VARCHAR(32) NOT NULL,
 project_id BIGINT NULL, agent_principal_id BIGINT NULL, source_kind VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL, locator VARCHAR(512) NOT NULL, format VARCHAR(16) NOT NULL,
 content TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, chunker_version INT NOT NULL DEFAULT 1,
 state VARCHAR(32) NOT NULL, state_reason VARCHAR(64) NOT NULL, chunk_count INT NOT NULL DEFAULT 0,
 text_chars INT NOT NULL DEFAULT 0, attempts INT NOT NULL DEFAULT 0, indexed_at DATETIME NULL,
 version INT NOT NULL DEFAULT 1, created_by BIGINT NOT NULL, updated_by BIGINT NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_source_scope CHECK ((scope='TEAM' AND project_id IS NULL AND agent_principal_id IS NULL) OR (scope='PROJECT' AND project_id IS NOT NULL AND agent_principal_id IS NULL) OR (scope='AGENT' AND agent_principal_id IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_knowledge_source_kind CHECK (source_kind IN ('UPLOAD','URL','REPOSITORY','REQUIREMENT','FEEDBACK','DECISION','DELIVERY')),
 CONSTRAINT {{.Prefix}}chk_knowledge_source_format CHECK (format IN ('MARKDOWN','TEXT','CODE','PDF')),
 CONSTRAINT {{.Prefix}}chk_knowledge_source_state CHECK (state IN ('PENDING','INDEXING','INDEXED','FAILED','INVALIDATED')),
 CONSTRAINT {{.Prefix}}chk_knowledge_source_counts CHECK (chunk_count >= 0 AND text_chars >= 0 AND attempts >= 0)
);
INSERT INTO {{.Prefix}}knowledge_sources_event (id,tenant_id,scope,project_id,agent_principal_id,source_kind,title,locator,format,content,content_hash,chunker_version,state,state_reason,chunk_count,text_chars,attempts,indexed_at,version,created_by,updated_by,created_at,updated_at,tags_json,archived_at) SELECT id,tenant_id,scope,project_id,agent_principal_id,source_kind,title,locator,format,content,content_hash,chunker_version,state,state_reason,chunk_count,text_chars,attempts,indexed_at,version,created_by,updated_by,created_at,updated_at,tags_json,archived_at FROM {{.Prefix}}knowledge_sources;
DROP TABLE {{.Prefix}}knowledge_sources;
ALTER TABLE {{.Prefix}}knowledge_sources_event RENAME TO {{.Prefix}}knowledge_sources;
CREATE INDEX {{.Prefix}}idx_knowledge_source_scope ON {{.Prefix}}knowledge_sources (tenant_id,scope,project_id,agent_principal_id,state,id);
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
