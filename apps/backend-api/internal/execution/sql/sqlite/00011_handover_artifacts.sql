-- +goose Up
CREATE TABLE {{.Prefix}}execution_handover_artifacts (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 retirement_id INTEGER NOT NULL,
 agent_principal_id INTEGER NOT NULL,
 execution_id INTEGER,
 source VARCHAR(16) NOT NULL,
 content TEXT NOT NULL,
 sha256 CHAR(64) NOT NULL,
 created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}handover_positive CHECK (id>0 AND tenant_id>0 AND retirement_id>0 AND agent_principal_id>0),
 CONSTRAINT {{.Prefix}}handover_source CHECK ((source='SYSTEM' AND execution_id IS NULL) OR (source='AGENT' AND execution_id IS NOT NULL AND execution_id>0)),
 CONSTRAINT {{.Prefix}}handover_producer UNIQUE(tenant_id,execution_id),
 CONSTRAINT {{.Prefix}}handover_execution FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id)
);
CREATE INDEX {{.Prefix}}handover_retirement ON {{.Prefix}}execution_handover_artifacts(tenant_id,retirement_id,id);
-- +goose Down
-- Refuse to erase immutable retirement documents.
CREATE TABLE IF NOT EXISTS {{.Prefix}}execution_handover_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}execution_handover_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_handover_artifacts;
DROP TABLE {{.Prefix}}execution_handover_rollback_guard;
DROP TABLE {{.Prefix}}execution_handover_artifacts;
