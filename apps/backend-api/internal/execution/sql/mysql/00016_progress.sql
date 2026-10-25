-- +goose Up
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN progress_json LONGTEXT NULL;

-- +goose Down
-- Persisted progress observations must not be silently discarded.
CREATE TABLE IF NOT EXISTS {{.Prefix}}execution_progress_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}execution_progress_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_executions WHERE progress_json IS NOT NULL;
DROP TABLE {{.Prefix}}execution_progress_rollback_guard;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN progress_json;
