-- +goose Up
CREATE TABLE {{.Prefix}}governance_automated_decisions (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 work_item_id INTEGER NOT NULL,
 claim_id INTEGER NOT NULL,
 trigger_event_id INTEGER NOT NULL,
 state VARCHAR(16) NOT NULL CHECK(state IN ('APPLIED','DENIED','UNAVAILABLE','SUPERSEDED')),
 reason VARCHAR(64) NOT NULL,
 actor_id INTEGER NOT NULL,
 authority_source VARCHAR(200) NOT NULL,
 decision_id INTEGER NOT NULL,
 created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_automated_claim UNIQUE(tenant_id,claim_id)
);
CREATE INDEX {{.Prefix}}governance_automated_project ON {{.Prefix}}governance_automated_decisions(tenant_id,project_id,work_item_id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_automated_decisions;
