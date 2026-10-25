-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_clarification_rounds (
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
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}ck_clarification_state CHECK (state IN ('OPEN','ANSWERED')),
 CONSTRAINT {{.Prefix}}ck_clarification_version CHECK (version > 0)
);
CREATE INDEX {{.Prefix}}idx_clarification_project ON {{.Prefix}}clientwork_clarification_rounds (tenant_id,project_id,id);
CREATE INDEX {{.Prefix}}idx_clarification_source ON {{.Prefix}}clientwork_clarification_rounds (tenant_id,project_id,intake_id,state);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_clarification_rounds;
