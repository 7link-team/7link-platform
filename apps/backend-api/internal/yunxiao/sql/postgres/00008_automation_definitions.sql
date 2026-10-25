-- +goose Up
ALTER TABLE {{.Prefix}}delivery_automation_rules ADD COLUMN definition TEXT NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}delivery_automation_rules ADD COLUMN archived_at TIMESTAMP(3) WITH TIME ZONE NULL;
ALTER TABLE {{.Prefix}}delivery_automation_runs ADD COLUMN definition_snapshot TEXT NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE {{.Prefix}}delivery_automation_runs DROP COLUMN definition_snapshot;
ALTER TABLE {{.Prefix}}delivery_automation_rules DROP COLUMN archived_at;
ALTER TABLE {{.Prefix}}delivery_automation_rules DROP COLUMN definition;
