-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_settings ADD COLUMN index_strategy VARCHAR(32) NOT NULL DEFAULT 'LEXICAL';
ALTER TABLE {{.Prefix}}knowledge_settings ADD COLUMN source_permission VARCHAR(32) NOT NULL DEFAULT 'WRITERS';
ALTER TABLE {{.Prefix}}knowledge_settings ADD COLUMN memory_approval VARCHAR(32) NOT NULL DEFAULT 'RULES_ONLY';

CREATE TABLE {{.Prefix}}knowledge_entries_policy (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, scope VARCHAR(32) NOT NULL,
 project_id BIGINT NULL, agent_principal_id BIGINT NULL, kind VARCHAR(32) NOT NULL, state VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL, body TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, provenance VARCHAR(512) NOT NULL,
 version INT NOT NULL DEFAULT 1, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_scope CHECK ((scope='TEAM' AND project_id IS NULL AND agent_principal_id IS NULL) OR (scope='PROJECT' AND project_id IS NOT NULL AND agent_principal_id IS NULL) OR (scope='AGENT' AND agent_principal_id IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_knowledge_kind CHECK (kind IN ('DOCUMENT','EXPERIENCE','RULE')),
 CONSTRAINT {{.Prefix}}chk_knowledge_state CHECK (state IN ('ACTIVE','PENDING_REVIEW','REJECTED','REVOKED')),
 CONSTRAINT {{.Prefix}}chk_knowledge_rule CHECK (kind IN ('RULE','EXPERIENCE') OR state IN ('ACTIVE','REVOKED'))
);
INSERT INTO {{.Prefix}}knowledge_entries_policy SELECT * FROM {{.Prefix}}knowledge_entries;
DROP TABLE {{.Prefix}}knowledge_entries;
ALTER TABLE {{.Prefix}}knowledge_entries_policy RENAME TO {{.Prefix}}knowledge_entries;
CREATE INDEX {{.Prefix}}idx_knowledge_scope ON {{.Prefix}}knowledge_entries (tenant_id,scope,project_id,agent_principal_id,state,id);
CREATE INDEX {{.Prefix}}idx_knowledge_queue ON {{.Prefix}}knowledge_entries (tenant_id,kind,state,id);

-- +goose Down
CREATE TABLE {{.Prefix}}knowledge_entries_policy (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, scope VARCHAR(32) NOT NULL,
 project_id BIGINT NULL, agent_principal_id BIGINT NULL, kind VARCHAR(32) NOT NULL, state VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL, body TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, provenance VARCHAR(512) NOT NULL,
 version INT NOT NULL DEFAULT 1, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_scope CHECK ((scope='TEAM' AND project_id IS NULL AND agent_principal_id IS NULL) OR (scope='PROJECT' AND project_id IS NOT NULL AND agent_principal_id IS NULL) OR (scope='AGENT' AND agent_principal_id IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_knowledge_kind CHECK (kind IN ('DOCUMENT','EXPERIENCE','RULE')),
 CONSTRAINT {{.Prefix}}chk_knowledge_state CHECK (state IN ('ACTIVE','PENDING_REVIEW','REJECTED','REVOKED')),
 CONSTRAINT {{.Prefix}}chk_knowledge_rule CHECK (kind='RULE' OR state IN ('ACTIVE','REVOKED'))
);
INSERT INTO {{.Prefix}}knowledge_entries_policy SELECT * FROM {{.Prefix}}knowledge_entries;
DROP TABLE {{.Prefix}}knowledge_entries;
ALTER TABLE {{.Prefix}}knowledge_entries_policy RENAME TO {{.Prefix}}knowledge_entries;
CREATE INDEX {{.Prefix}}idx_knowledge_scope ON {{.Prefix}}knowledge_entries (tenant_id,scope,project_id,agent_principal_id,state,id);
CREATE INDEX {{.Prefix}}idx_knowledge_queue ON {{.Prefix}}knowledge_entries (tenant_id,kind,state,id);

ALTER TABLE {{.Prefix}}knowledge_settings DROP COLUMN memory_approval;
ALTER TABLE {{.Prefix}}knowledge_settings DROP COLUMN source_permission;
ALTER TABLE {{.Prefix}}knowledge_settings DROP COLUMN index_strategy;
