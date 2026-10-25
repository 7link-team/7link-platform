-- +goose Up
ALTER TABLE {{.Prefix}}delivery_automation_rules ADD COLUMN definition VARCHAR(6000) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}delivery_automation_rules ADD COLUMN archived_at DATETIME(3) NULL;
ALTER TABLE {{.Prefix}}delivery_automation_runs ADD COLUMN definition_snapshot VARCHAR(6000) NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE {{.Prefix}}delivery_automation_runs DROP COLUMN definition_snapshot;
ALTER TABLE {{.Prefix}}delivery_automation_rules DROP COLUMN archived_at;
ALTER TABLE {{.Prefix}}delivery_automation_rules DROP COLUMN definition;
