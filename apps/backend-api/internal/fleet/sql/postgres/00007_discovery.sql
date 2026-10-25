-- +goose Up
ALTER TABLE {{.Prefix}}fleet_inspections ADD COLUMN result_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE {{.Prefix}}fleet_inspections ADD COLUMN scope_key VARCHAR(40) NOT NULL DEFAULT 'home';
ALTER TABLE {{.Prefix}}fleet_inspections ADD COLUMN workdir_path VARCHAR(1024) NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_inspections DROP COLUMN workdir_path;
ALTER TABLE {{.Prefix}}fleet_inspections DROP COLUMN scope_key;
ALTER TABLE {{.Prefix}}fleet_inspections DROP COLUMN result_json;
