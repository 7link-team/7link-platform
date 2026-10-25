-- +goose Up
CREATE TABLE {{.Prefix}}governance_claim_evaluations (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 work_item_id INTEGER NOT NULL,
 claim_id INTEGER NOT NULL,
 trigger_event_id INTEGER NOT NULL,
 low_confidence_result VARCHAR(16) NOT NULL CHECK(low_confidence_result IN ('PASS','TRIGGERED','SKIPPED')),
 sensitive_path_result VARCHAR(16) NOT NULL CHECK(sensitive_path_result IN ('PASS','TRIGGERED','UNAVAILABLE','SKIPPED')),
 evaluated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_claim_evaluation_claim UNIQUE(tenant_id,claim_id)
);
CREATE INDEX {{.Prefix}}governance_claim_evaluation_work ON {{.Prefix}}governance_claim_evaluations(tenant_id,project_id,work_item_id,claim_id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_claim_evaluations;
