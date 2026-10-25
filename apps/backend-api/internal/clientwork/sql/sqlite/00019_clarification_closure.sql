-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_clarification_rounds_rebuild (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 intake_id BIGINT NOT NULL,
 intake_version INT NOT NULL,
 recipient_id BIGINT NOT NULL,
 state VARCHAR(32) NOT NULL,
 questions TEXT NOT NULL,
 version INT NOT NULL,
 created_by BIGINT NOT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 closed_at DATETIME,
 close_reason VARCHAR(32),
 close_note VARCHAR(2000),
 CONSTRAINT {{.Prefix}}ck_clarification_state CHECK (state IN ('OPEN','ANSWERED','CANCELLED')),
 CONSTRAINT {{.Prefix}}ck_clarification_closure CHECK ((state='OPEN' AND closed_at IS NULL AND close_reason IS NULL AND close_note IS NULL) OR (state='ANSWERED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='ALL_ANSWERED') OR (state='CANCELLED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='CANCELLED_BY_TEAM' AND close_note IS NOT NULL AND length(close_note) BETWEEN 1 AND 2000)),
 CONSTRAINT {{.Prefix}}ck_clarification_version CHECK (version > 0)
);
INSERT INTO {{.Prefix}}clientwork_clarification_rounds_rebuild (id,tenant_id,project_id,intake_id,intake_version,recipient_id,state,questions,version,created_by,created_at,updated_at,closed_at,close_reason) SELECT id,tenant_id,project_id,intake_id,intake_version,recipient_id,state,questions,version,created_by,created_at,updated_at,CASE WHEN state='ANSWERED' THEN updated_at END,CASE WHEN state='ANSWERED' THEN 'ALL_ANSWERED' END FROM {{.Prefix}}clientwork_clarification_rounds;
DROP TABLE {{.Prefix}}clientwork_clarification_rounds;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds_rebuild RENAME TO {{.Prefix}}clientwork_clarification_rounds;
CREATE INDEX {{.Prefix}}idx_clarification_project ON {{.Prefix}}clientwork_clarification_rounds (tenant_id,project_id,id);
CREATE INDEX {{.Prefix}}idx_clarification_source ON {{.Prefix}}clientwork_clarification_rounds (tenant_id,project_id,intake_id,state);
-- +goose Down
-- Existing cancellations prevent rollback; no history is silently rewritten.
CREATE TABLE {{.Prefix}}clientwork_clarification_rounds_rebuild (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 intake_id BIGINT NOT NULL,
 intake_version INT NOT NULL,
 recipient_id BIGINT NOT NULL,
 state VARCHAR(32) NOT NULL,
 questions TEXT NOT NULL,
 version INT NOT NULL,
 created_by BIGINT NOT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}ck_clarification_state CHECK (state IN ('OPEN','ANSWERED')),
 CONSTRAINT {{.Prefix}}ck_clarification_version CHECK (version > 0)
);
INSERT INTO {{.Prefix}}clientwork_clarification_rounds_rebuild (id,tenant_id,project_id,intake_id,intake_version,recipient_id,state,questions,version,created_by,created_at,updated_at) SELECT id,tenant_id,project_id,intake_id,intake_version,recipient_id,state,questions,version,created_by,created_at,updated_at FROM {{.Prefix}}clientwork_clarification_rounds;
DROP TABLE {{.Prefix}}clientwork_clarification_rounds;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds_rebuild RENAME TO {{.Prefix}}clientwork_clarification_rounds;
CREATE INDEX {{.Prefix}}idx_clarification_project ON {{.Prefix}}clientwork_clarification_rounds (tenant_id,project_id,id);
CREATE INDEX {{.Prefix}}idx_clarification_source ON {{.Prefix}}clientwork_clarification_rounds (tenant_id,project_id,intake_id,state);
