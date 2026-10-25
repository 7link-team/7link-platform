-- +goose Up
CREATE TABLE {{.Prefix}}governance_workflow_decisions (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 binding_id BIGINT NOT NULL,
 definition_hash VARCHAR(64) NOT NULL,
 expansion_key VARCHAR(512) NOT NULL,
 gate VARCHAR(16) NOT NULL CHECK(gate IN ('TEAM','CLIENT')),
 outcome VARCHAR(16) NOT NULL CHECK(outcome IN ('APPROVE','REJECT')),
 reason VARCHAR(2000) NOT NULL,
 actor_id BIGINT NOT NULL,
 authority_source VARCHAR(160) NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}governance_workflow_decision_node UNIQUE(tenant_id,binding_id,expansion_key)
);
-- +goose Down
DROP TABLE {{.Prefix}}governance_workflow_decisions;
