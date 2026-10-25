-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_settings (
 tenant_id BIGINT NOT NULL PRIMARY KEY, candidate_limit INT NOT NULL DEFAULT 50,
 version INT NOT NULL DEFAULT 1, updated_by BIGINT NOT NULL, updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_candidate_limit CHECK (candidate_limit BETWEEN 1 AND 200)
);
CREATE TABLE {{.Prefix}}knowledge_sources (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, scope VARCHAR(32) NOT NULL,
 project_id BIGINT NULL, agent_principal_id BIGINT NULL, source_kind VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL, locator VARCHAR(512) NOT NULL, format VARCHAR(16) NOT NULL,
 content MEDIUMTEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, chunker_version INT NOT NULL DEFAULT 1,
 state VARCHAR(32) NOT NULL, state_reason VARCHAR(64) NOT NULL, chunk_count INT NOT NULL DEFAULT 0,
 text_chars INT NOT NULL DEFAULT 0, attempts INT NOT NULL DEFAULT 0, indexed_at DATETIME(3) NULL,
 version INT NOT NULL DEFAULT 1, created_by BIGINT NOT NULL, updated_by BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_source_scope CHECK ((scope='TEAM' AND project_id IS NULL AND agent_principal_id IS NULL) OR (scope='PROJECT' AND project_id IS NOT NULL AND agent_principal_id IS NULL) OR (scope='AGENT' AND agent_principal_id IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_knowledge_source_kind CHECK (source_kind IN ('UPLOAD','URL','REPOSITORY')),
 CONSTRAINT {{.Prefix}}chk_knowledge_source_format CHECK (format IN ('MARKDOWN','TEXT','CODE','PDF')),
 CONSTRAINT {{.Prefix}}chk_knowledge_source_state CHECK (state IN ('PENDING','INDEXING','INDEXED','FAILED','INVALIDATED')),
 CONSTRAINT {{.Prefix}}chk_knowledge_source_counts CHECK (chunk_count >= 0 AND text_chars >= 0 AND attempts >= 0)
);
CREATE INDEX {{.Prefix}}idx_knowledge_source_scope ON {{.Prefix}}knowledge_sources (tenant_id,scope,project_id,agent_principal_id,state,id);
CREATE TABLE {{.Prefix}}knowledge_chunks (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, source_id BIGINT NOT NULL, entry_id BIGINT NOT NULL,
 ordinal INT NOT NULL, kind VARCHAR(8) NOT NULL, heading_path VARCHAR(300) NOT NULL,
 content TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, char_count INT NOT NULL, token_estimate INT NOT NULL,
 invalidated_at DATETIME(3) NULL, live_slot SMALLINT NULL, created_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_chunk_live CHECK ((live_slot IS NOT NULL AND live_slot=1 AND invalidated_at IS NULL) OR (live_slot IS NULL AND invalidated_at IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_knowledge_chunk_kind CHECK (kind IN ('TEXT','CODE')),
 CONSTRAINT {{.Prefix}}uq_knowledge_chunk_live UNIQUE (source_id,content_hash,live_slot),
 CONSTRAINT {{.Prefix}}uq_knowledge_chunk_entry UNIQUE (entry_id)
);
CREATE INDEX {{.Prefix}}idx_knowledge_chunk_source ON {{.Prefix}}knowledge_chunks (tenant_id,source_id,invalidated_at,ordinal);
-- +goose Down
DROP TABLE {{.Prefix}}knowledge_chunks;
DROP TABLE {{.Prefix}}knowledge_sources;
DROP TABLE {{.Prefix}}knowledge_settings;
