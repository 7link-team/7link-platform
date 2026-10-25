-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_intake_suggestions (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 intake_id BIGINT NOT NULL,
 intake_version INT NOT NULL,
 proposal TEXT NOT NULL,
 actor_kind VARCHAR(10) NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}ck_intake_suggestion_ids CHECK (id>0 AND tenant_id>0 AND project_id>0 AND intake_id>0 AND intake_version>0 AND actor_id>0),
 CONSTRAINT {{.Prefix}}ck_intake_suggestion_actor CHECK (actor_kind IN ('HUMAN','AGENT'))
);
CREATE INDEX {{.Prefix}}idx_intake_suggestion_source ON {{.Prefix}}clientwork_intake_suggestions (tenant_id,project_id,intake_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_intake_suggestions;
