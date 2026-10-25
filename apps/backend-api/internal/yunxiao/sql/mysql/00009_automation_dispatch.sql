-- +goose Up
ALTER TABLE {{.Prefix}}delivery_automation_runs ADD COLUMN run_as VARCHAR(100) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}delivery_automation_runs ADD COLUMN actions_result VARCHAR(3000) NOT NULL DEFAULT '[]';
ALTER TABLE {{.Prefix}}delivery_automation_runs ADD COLUMN trigger_key VARCHAR(160) NULL;
CREATE UNIQUE INDEX {{.Prefix}}uq_automation_run_source ON {{.Prefix}}delivery_automation_runs(tenant_id,rule_id,trigger_key);
-- +goose Down
DROP INDEX {{.Prefix}}uq_automation_run_source ON {{.Prefix}}delivery_automation_runs;
ALTER TABLE {{.Prefix}}delivery_automation_runs DROP COLUMN trigger_key;
ALTER TABLE {{.Prefix}}delivery_automation_runs DROP COLUMN actions_result;
ALTER TABLE {{.Prefix}}delivery_automation_runs DROP COLUMN run_as;
