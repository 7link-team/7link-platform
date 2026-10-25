-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_injection_logs (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL, agent_principal_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL,
 result_json TEXT NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_injection_ids CHECK (tenant_id > 0 AND project_id > 0 AND agent_principal_id > 0 AND execution_id > 0 AND work_item_id > 0)
);
CREATE INDEX {{.Prefix}}idx_knowledge_injection_execution ON {{.Prefix}}knowledge_injection_logs (tenant_id, execution_id, id);
-- +goose Down
DROP TABLE {{.Prefix}}knowledge_injection_logs;
