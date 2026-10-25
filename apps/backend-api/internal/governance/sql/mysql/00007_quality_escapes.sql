-- +goose Up
CREATE TABLE {{.Prefix}}governance_quality_escapes (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 source_event_id BIGINT NOT NULL,
 escape_key VARCHAR(64) NOT NULL,
 stage VARCHAR(32) NOT NULL,
 category VARCHAR(64) NOT NULL,
 summary VARCHAR(2000) NOT NULL,
 passed_by TEXT NOT NULL,
 learning_state VARCHAR(16) NOT NULL,
 memory_ids TEXT NOT NULL,
 eval_case_ref VARCHAR(200),
 decision_reason VARCHAR(2000),
 decided_by BIGINT,
 decided_at DATETIME(3),
 created_at DATETIME(3) NOT NULL,
 version INT NOT NULL,
 CONSTRAINT {{.Prefix}}governance_escape_source UNIQUE(source_event_id),
 CONSTRAINT {{.Prefix}}governance_escape_key UNIQUE(tenant_id,escape_key),
 CONSTRAINT {{.Prefix}}governance_escape_stage CHECK(stage IN ('HUMAN_REVIEW','CLIENT_ACCEPTANCE')),
 CONSTRAINT {{.Prefix}}governance_escape_state CHECK(learning_state IN ('PENDING','RECORDED','DISMISSED')),
 CONSTRAINT {{.Prefix}}governance_escape_version CHECK(version > 0),
 CONSTRAINT {{.Prefix}}governance_escape_decision CHECK((learning_state='PENDING' AND decided_by IS NULL AND decided_at IS NULL) OR (learning_state IN ('RECORDED','DISMISSED') AND decided_by IS NOT NULL AND decided_at IS NOT NULL))
);
CREATE INDEX {{.Prefix}}governance_escape_list ON {{.Prefix}}governance_quality_escapes(tenant_id,project_id,learning_state,id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_quality_escapes;
