-- +goose Up
CREATE TABLE {{.Prefix}}governance_automated_decisions (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 claim_id BIGINT NOT NULL,
 trigger_event_id BIGINT NOT NULL,
 state VARCHAR(16) NOT NULL CHECK(state IN ('APPLIED','DENIED','UNAVAILABLE','SUPERSEDED')),
 reason VARCHAR(64) NOT NULL,
 actor_id BIGINT NOT NULL,
 authority_source VARCHAR(200) NOT NULL,
 decision_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}governance_automated_claim UNIQUE(tenant_id,claim_id)
);
CREATE INDEX {{.Prefix}}governance_automated_project ON {{.Prefix}}governance_automated_decisions(tenant_id,project_id,work_item_id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_automated_decisions;
