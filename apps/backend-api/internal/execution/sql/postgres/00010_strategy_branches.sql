-- +goose Up
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN strategy_parent_id BIGINT CHECK (strategy_parent_id IS NULL OR (strategy_parent_id > 0 AND strategy_parent_id <> id AND branch_reason = 'STRATEGY' AND parent_execution_id IS NULL AND source_decision_id IS NULL));
CREATE INDEX {{.Prefix}}execution_work_history ON {{.Prefix}}execution_executions(tenant_id,project_id,work_item_id,id);
-- +goose Down
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}execution_strategy_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}execution_strategy_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_executions WHERE strategy_parent_id IS NOT NULL;
DROP TABLE {{.Prefix}}execution_strategy_rollback_guard;
DROP INDEX {{.Prefix}}execution_work_history;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN strategy_parent_id;
