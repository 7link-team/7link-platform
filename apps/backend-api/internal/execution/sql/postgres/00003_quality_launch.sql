-- +goose Up
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN parent_execution_id BIGINT CHECK (parent_execution_id IS NULL OR parent_execution_id > 0);
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN source_decision_id BIGINT CHECK ((source_decision_id IS NULL AND parent_execution_id IS NULL) OR (source_decision_id IS NOT NULL AND parent_execution_id IS NOT NULL AND source_decision_id > 0 AND parent_execution_id > 0));
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN branch_reason VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN start_point VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN feedback_snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN autostart BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX {{.Prefix}}execution_quality_decision ON {{.Prefix}}execution_executions(tenant_id,source_decision_id);
-- +goose Down
-- Immutable source decisions and feedback cannot be silently erased.
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}execution_quality_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}execution_quality_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_executions WHERE source_decision_id IS NOT NULL OR parent_execution_id IS NOT NULL OR feedback_snapshot <> '' OR branch_reason <> '' OR start_point <> '' OR autostart;
DROP TABLE {{.Prefix}}execution_quality_rollback_guard;
DROP INDEX {{.Prefix}}execution_quality_decision;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN autostart;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN feedback_snapshot;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN start_point;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN branch_reason;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN source_decision_id;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN parent_execution_id;
