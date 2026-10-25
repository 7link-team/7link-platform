-- +goose Up
ALTER TABLE {{.Prefix}}fleet_workdirs ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE {{.Prefix}}fleet_workdirs ADD COLUMN archived_at TIMESTAMPTZ(3) NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_workdirs DROP COLUMN archived_at;
ALTER TABLE {{.Prefix}}fleet_workdirs DROP COLUMN version;
