-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN ttl_days INT NOT NULL DEFAULT 180;
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN expires_at DATETIME(3) NULL;
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN last_used_at DATETIME(3) NULL;
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN use_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN source_execution_id BIGINT NULL;
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN supersedes_id BIGINT NULL;
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN risk_flags VARCHAR(256) NOT NULL DEFAULT '[]';
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN origin VARCHAR(32) NOT NULL DEFAULT 'HUMAN';
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN live_slot INT NULL;
ALTER TABLE {{.Prefix}}knowledge_entries DROP CHECK {{.Prefix}}chk_knowledge_state;
ALTER TABLE {{.Prefix}}knowledge_entries ADD CONSTRAINT {{.Prefix}}chk_knowledge_state CHECK (state IN ('ACTIVE','PENDING_REVIEW','REJECTED','REVOKED','EXPIRED','SUPERSEDED'));
UPDATE {{.Prefix}}knowledge_entries SET live_slot=1 WHERE state IN ('ACTIVE','PENDING_REVIEW') AND scope='AGENT';
ALTER TABLE {{.Prefix}}knowledge_entries ADD COLUMN memory_project_key BIGINT GENERATED ALWAYS AS (COALESCE(project_id,0)) STORED;
CREATE UNIQUE INDEX {{.Prefix}}uq_knowledge_memory_live ON {{.Prefix}}knowledge_entries (tenant_id,agent_principal_id,memory_project_key,content_hash,live_slot);
CREATE INDEX {{.Prefix}}idx_knowledge_memory_expiry ON {{.Prefix}}knowledge_entries (tenant_id,state,expires_at);

UPDATE {{.Prefix}}knowledge_entries SET expires_at=DATE_ADD(created_at, INTERVAL 180 DAY) WHERE scope='AGENT' AND kind='EXPERIENCE';
ALTER TABLE {{.Prefix}}knowledge_reviews DROP CHECK {{.Prefix}}chk_knowledge_review_action;
ALTER TABLE {{.Prefix}}knowledge_reviews ADD CONSTRAINT {{.Prefix}}chk_knowledge_review_action CHECK (action IN ('WRITTEN','ESCALATED','APPROVED','REJECTED','EDITED','REVOKED','EXPIRED','PROMOTED','SUPERSEDED'));

-- +goose Down
UPDATE {{.Prefix}}knowledge_entries SET state='REVOKED' WHERE state IN ('EXPIRED','SUPERSEDED');
ALTER TABLE {{.Prefix}}knowledge_entries DROP CHECK {{.Prefix}}chk_knowledge_state;
ALTER TABLE {{.Prefix}}knowledge_entries ADD CONSTRAINT {{.Prefix}}chk_knowledge_state CHECK (state IN ('ACTIVE','PENDING_REVIEW','REJECTED','REVOKED'));
DROP INDEX {{.Prefix}}uq_knowledge_memory_live ON {{.Prefix}}knowledge_entries;
DROP INDEX {{.Prefix}}idx_knowledge_memory_expiry ON {{.Prefix}}knowledge_entries;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN memory_project_key;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN live_slot;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN origin;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN risk_flags;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN supersedes_id;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN source_execution_id;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN use_count;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN last_used_at;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN expires_at;
ALTER TABLE {{.Prefix}}knowledge_entries DROP COLUMN ttl_days;
UPDATE {{.Prefix}}knowledge_reviews SET action='REVOKED' WHERE action NOT IN ('WRITTEN','APPROVED','REJECTED','REVOKED');
ALTER TABLE {{.Prefix}}knowledge_reviews DROP CHECK {{.Prefix}}chk_knowledge_review_action;
ALTER TABLE {{.Prefix}}knowledge_reviews ADD CONSTRAINT {{.Prefix}}chk_knowledge_review_action CHECK (action IN ('WRITTEN','APPROVED','REJECTED','REVOKED'));
