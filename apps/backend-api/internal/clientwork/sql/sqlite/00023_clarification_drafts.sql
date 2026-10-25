-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_clarification_drafts (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 intake_id BIGINT NOT NULL,
 intake_version INT NOT NULL,
 questions TEXT NOT NULL,
 actor_kind VARCHAR(16) NOT NULL,
 actor_id BIGINT NOT NULL,
 version INT NOT NULL,
 sent_round_id BIGINT NULL,
 created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}ck_clarification_draft_actor CHECK (actor_kind IN ('HUMAN','AGENT')),
 CONSTRAINT {{.Prefix}}ck_clarification_draft_version CHECK ((version=1 AND sent_round_id IS NULL) OR (version=2 AND sent_round_id IS NOT NULL))
);
CREATE INDEX {{.Prefix}}idx_clarification_draft_source ON {{.Prefix}}clientwork_clarification_drafts (tenant_id,project_id,intake_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_clarification_drafts;
