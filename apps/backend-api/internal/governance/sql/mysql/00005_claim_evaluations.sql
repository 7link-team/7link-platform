-- +goose Up
CREATE TABLE {{.Prefix}}governance_claim_evaluations (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 claim_id BIGINT NOT NULL,
 trigger_event_id BIGINT NOT NULL,
 low_confidence_result VARCHAR(16) NOT NULL CHECK(low_confidence_result IN ('PASS','TRIGGERED','SKIPPED')),
 sensitive_path_result VARCHAR(16) NOT NULL CHECK(sensitive_path_result IN ('PASS','TRIGGERED','UNAVAILABLE','SKIPPED')),
 evaluated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}governance_claim_evaluation_claim UNIQUE(tenant_id,claim_id)
);
CREATE INDEX {{.Prefix}}governance_claim_evaluation_work ON {{.Prefix}}governance_claim_evaluations(tenant_id,project_id,work_item_id,claim_id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_claim_evaluations;
