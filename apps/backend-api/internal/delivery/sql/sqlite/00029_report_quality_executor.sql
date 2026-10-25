-- +goose Up
-- Historical receipts remain unknown: accountable ownership is not execution.
ALTER TABLE {{.Prefix}}delivery_report_quality_receipts ADD COLUMN executor_principal_id BIGINT NULL;

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_quality_executor_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_quality_executor_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_report_quality_receipts WHERE executor_principal_id IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_quality_executor_rollback_guard;
ALTER TABLE {{.Prefix}}delivery_report_quality_receipts DROP COLUMN executor_principal_id;
