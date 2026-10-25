-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_diagnostic_logs (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, actor_id BIGINT NOT NULL,
 scope VARCHAR(16) NOT NULL, project_id BIGINT NULL, agent_principal_id BIGINT NULL,
 query_text VARCHAR(500) NOT NULL, result_json TEXT NOT NULL, latency_ms REAL NOT NULL,
 created_at DATETIME NOT NULL
);
CREATE INDEX {{.Prefix}}idx_knowledge_diagnostics_scope ON {{.Prefix}}knowledge_diagnostic_logs (tenant_id, scope, project_id, agent_principal_id, created_at);
CREATE TABLE {{.Prefix}}knowledge_runtime_metric_samples (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NULL,
 agent_principal_id BIGINT NULL, kind VARCHAR(24) NOT NULL, execution_id BIGINT NULL, evidence_id BIGINT NOT NULL,
 entry_id BIGINT NULL, hit BOOLEAN NOT NULL, latency_ms REAL NULL,
 outcome VARCHAR(16) NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}uq_knowledge_runtime_sample UNIQUE (tenant_id,kind,execution_id,evidence_id,entry_id),
 CONSTRAINT {{.Prefix}}chk_knowledge_runtime_sample CHECK (
  (project_id IS NULL OR project_id > 0) AND
  (agent_principal_id IS NULL OR agent_principal_id > 0) AND
  ((kind = 'INJECTION' AND project_id IS NOT NULL AND execution_id IS NOT NULL AND execution_id > 0 AND outcome IS NULL AND entry_id IS NULL) OR
   (kind = 'SEARCH' AND outcome IS NULL AND entry_id IS NULL AND execution_id IS NULL) OR
   (kind = 'CITATION_OUTCOME' AND project_id IS NOT NULL AND execution_id IS NOT NULL AND execution_id > 0 AND outcome IS NOT NULL AND outcome IN ('ACCEPTED','REJECTED') AND entry_id IS NOT NULL AND entry_id > 0)))
);
CREATE INDEX {{.Prefix}}idx_knowledge_runtime_sample_window ON {{.Prefix}}knowledge_runtime_metric_samples (tenant_id, project_id, created_at);
-- +goose Down
DROP TABLE {{.Prefix}}knowledge_runtime_metric_samples;
DROP TABLE {{.Prefix}}knowledge_diagnostic_logs;
