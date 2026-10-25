-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_entries_memory (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, scope VARCHAR(32) NOT NULL,
 project_id BIGINT NULL, agent_principal_id BIGINT NULL, kind VARCHAR(32) NOT NULL, state VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL, body TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, provenance VARCHAR(512) NOT NULL,
 ttl_days INT NOT NULL DEFAULT 180,
 expires_at DATETIME NULL,
 last_used_at DATETIME NULL,
 use_count BIGINT NOT NULL DEFAULT 0,
 source_execution_id BIGINT NULL,
 supersedes_id BIGINT NULL,
 risk_flags VARCHAR(256) NOT NULL DEFAULT '[]',
 origin VARCHAR(32) NOT NULL DEFAULT 'HUMAN',
 live_slot INT NULL,
 version INT NOT NULL DEFAULT 1, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_scope CHECK ((scope='TEAM' AND project_id IS NULL AND agent_principal_id IS NULL) OR (scope='PROJECT' AND project_id IS NOT NULL AND agent_principal_id IS NULL) OR (scope='AGENT' AND agent_principal_id IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_knowledge_kind CHECK (kind IN ('DOCUMENT','EXPERIENCE','RULE')),
 CONSTRAINT {{.Prefix}}chk_knowledge_state CHECK (state IN ('ACTIVE','PENDING_REVIEW','REJECTED','REVOKED','EXPIRED','SUPERSEDED')),
 CONSTRAINT {{.Prefix}}chk_knowledge_rule CHECK (kind IN ('RULE','EXPERIENCE') OR state IN ('ACTIVE','REVOKED'))
);
INSERT INTO {{.Prefix}}knowledge_entries_memory (id,tenant_id,scope,project_id,agent_principal_id,kind,state,title,body,content_hash,provenance,version,created_by,created_at,updated_at) SELECT id,tenant_id,scope,project_id,agent_principal_id,kind,state,title,body,content_hash,provenance,version,created_by,created_at,updated_at FROM {{.Prefix}}knowledge_entries;
DROP TABLE {{.Prefix}}knowledge_entries;
ALTER TABLE {{.Prefix}}knowledge_entries_memory RENAME TO {{.Prefix}}knowledge_entries;
CREATE INDEX {{.Prefix}}idx_knowledge_scope ON {{.Prefix}}knowledge_entries (tenant_id,scope,project_id,agent_principal_id,state,id);
CREATE INDEX {{.Prefix}}idx_knowledge_queue ON {{.Prefix}}knowledge_entries (tenant_id,kind,state,id);
UPDATE {{.Prefix}}knowledge_entries SET live_slot=1 WHERE state IN ('ACTIVE','PENDING_REVIEW') AND scope='AGENT';
CREATE UNIQUE INDEX {{.Prefix}}uq_knowledge_memory_live ON {{.Prefix}}knowledge_entries (tenant_id,agent_principal_id,COALESCE(project_id,0),content_hash,live_slot);
CREATE INDEX {{.Prefix}}idx_knowledge_memory_expiry ON {{.Prefix}}knowledge_entries (tenant_id,state,expires_at);

UPDATE {{.Prefix}}knowledge_entries SET expires_at=datetime(created_at,'+180 days') WHERE scope='AGENT' AND kind='EXPERIENCE';
CREATE TABLE {{.Prefix}}knowledge_reviews_memory (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, entry_id BIGINT NOT NULL,
 action VARCHAR(32) NOT NULL, from_state VARCHAR(32) NOT NULL, to_state VARCHAR(32) NOT NULL,
 reason VARCHAR(2000) NOT NULL, actor_id BIGINT NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_review_action CHECK (action IN ('WRITTEN','ESCALATED','APPROVED','REJECTED','EDITED','REVOKED','EXPIRED','PROMOTED','SUPERSEDED'))
);
INSERT INTO {{.Prefix}}knowledge_reviews_memory SELECT * FROM {{.Prefix}}knowledge_reviews;
DROP TABLE {{.Prefix}}knowledge_reviews;
ALTER TABLE {{.Prefix}}knowledge_reviews_memory RENAME TO {{.Prefix}}knowledge_reviews;
CREATE INDEX {{.Prefix}}idx_knowledge_review_entry ON {{.Prefix}}knowledge_reviews (tenant_id,entry_id,id);

-- +goose Down
UPDATE {{.Prefix}}knowledge_entries SET state='REVOKED' WHERE state IN ('EXPIRED','SUPERSEDED');
DROP INDEX {{.Prefix}}uq_knowledge_memory_live;
DROP INDEX {{.Prefix}}idx_knowledge_memory_expiry;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN live_slot;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN origin;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN risk_flags;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN supersedes_id;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN source_execution_id;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN use_count;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN last_used_at;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN expires_at;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN ttl_days;
CREATE TABLE {{.Prefix}}knowledge_entries_memory (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, scope VARCHAR(32) NOT NULL,
 project_id BIGINT NULL, agent_principal_id BIGINT NULL, kind VARCHAR(32) NOT NULL, state VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL, body TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, provenance VARCHAR(512) NOT NULL,
 version INT NOT NULL DEFAULT 1, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_scope CHECK ((scope='TEAM' AND project_id IS NULL AND agent_principal_id IS NULL) OR (scope='PROJECT' AND project_id IS NOT NULL AND agent_principal_id IS NULL) OR (scope='AGENT' AND agent_principal_id IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_knowledge_kind CHECK (kind IN ('DOCUMENT','EXPERIENCE','RULE')),
 CONSTRAINT {{.Prefix}}chk_knowledge_state CHECK (state IN ('ACTIVE','PENDING_REVIEW','REJECTED','REVOKED')),
 CONSTRAINT {{.Prefix}}chk_knowledge_rule CHECK (kind IN ('RULE','EXPERIENCE') OR state IN ('ACTIVE','REVOKED'))
);
INSERT INTO {{.Prefix}}knowledge_entries_memory SELECT * FROM {{.Prefix}}knowledge_entries;
DROP TABLE {{.Prefix}}knowledge_entries;
ALTER TABLE {{.Prefix}}knowledge_entries_memory RENAME TO {{.Prefix}}knowledge_entries;
CREATE INDEX {{.Prefix}}idx_knowledge_scope ON {{.Prefix}}knowledge_entries (tenant_id,scope,project_id,agent_principal_id,state,id);
CREATE INDEX {{.Prefix}}idx_knowledge_queue ON {{.Prefix}}knowledge_entries (tenant_id,kind,state,id);
UPDATE {{.Prefix}}knowledge_reviews SET action='REVOKED' WHERE action NOT IN ('WRITTEN','APPROVED','REJECTED','REVOKED');
CREATE TABLE {{.Prefix}}knowledge_reviews_memory (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, entry_id BIGINT NOT NULL,
 action VARCHAR(32) NOT NULL, from_state VARCHAR(32) NOT NULL, to_state VARCHAR(32) NOT NULL,
 reason VARCHAR(2000) NOT NULL, actor_id BIGINT NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_review_action CHECK (action IN ('WRITTEN','APPROVED','REJECTED','REVOKED'))
);
INSERT INTO {{.Prefix}}knowledge_reviews_memory SELECT * FROM {{.Prefix}}knowledge_reviews;
DROP TABLE {{.Prefix}}knowledge_reviews;
ALTER TABLE {{.Prefix}}knowledge_reviews_memory RENAME TO {{.Prefix}}knowledge_reviews;
CREATE INDEX {{.Prefix}}idx_knowledge_review_entry ON {{.Prefix}}knowledge_reviews (tenant_id,entry_id,id);
