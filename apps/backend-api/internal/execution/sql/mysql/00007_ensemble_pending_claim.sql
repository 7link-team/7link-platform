-- +goose Up
ALTER TABLE {{.Prefix}}execution_executions ADD COLUMN pending_claim_json TEXT NULL;
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}execution_pending_claim_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}execution_pending_claim_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_executions WHERE pending_claim_json IS NOT NULL AND pending_claim_json<>'';
DROP TABLE {{.Prefix}}execution_pending_claim_rollback_guard;
ALTER TABLE {{.Prefix}}execution_executions DROP COLUMN pending_claim_json;
