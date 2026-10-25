-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_entries (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, scope VARCHAR(32) NOT NULL,
 project_id BIGINT NULL, agent_principal_id BIGINT NULL, kind VARCHAR(32) NOT NULL, state VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL, body TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, provenance VARCHAR(512) NOT NULL,
 version INT NOT NULL DEFAULT 1, created_by BIGINT NOT NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_scope CHECK ((scope='TEAM' AND project_id IS NULL AND agent_principal_id IS NULL) OR (scope='PROJECT' AND project_id IS NOT NULL AND agent_principal_id IS NULL) OR (scope='AGENT' AND agent_principal_id IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_knowledge_kind CHECK (kind IN ('DOCUMENT','EXPERIENCE','RULE')),
 CONSTRAINT {{.Prefix}}chk_knowledge_state CHECK (state IN ('ACTIVE','PENDING_REVIEW','REJECTED','REVOKED')),
 CONSTRAINT {{.Prefix}}chk_knowledge_rule CHECK (kind='RULE' OR state IN ('ACTIVE','REVOKED'))
);
CREATE INDEX {{.Prefix}}idx_knowledge_scope ON {{.Prefix}}knowledge_entries (tenant_id,scope,project_id,agent_principal_id,state,id);
CREATE INDEX {{.Prefix}}idx_knowledge_queue ON {{.Prefix}}knowledge_entries (tenant_id,kind,state,id);
CREATE TABLE {{.Prefix}}knowledge_reviews (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, entry_id BIGINT NOT NULL,
 action VARCHAR(32) NOT NULL, from_state VARCHAR(32) NOT NULL, to_state VARCHAR(32) NOT NULL,
 reason VARCHAR(2000) NOT NULL, actor_id BIGINT NOT NULL, created_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_review_action CHECK (action IN ('WRITTEN','APPROVED','REJECTED','REVOKED'))
);
CREATE INDEX {{.Prefix}}idx_knowledge_review_entry ON {{.Prefix}}knowledge_reviews (tenant_id,entry_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}knowledge_reviews;
DROP TABLE {{.Prefix}}knowledge_entries;
