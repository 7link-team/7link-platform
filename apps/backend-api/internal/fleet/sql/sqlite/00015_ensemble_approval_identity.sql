-- +goose Up
ALTER TABLE {{.Prefix}}fleet_ensemble_plans ADD COLUMN approval_requested_at DATETIME NULL;
ALTER TABLE {{.Prefix}}fleet_ensemble_plans ADD COLUMN approved_by BIGINT NULL;
ALTER TABLE {{.Prefix}}fleet_ensemble_plans ADD COLUMN approved_at DATETIME NULL;
ALTER TABLE {{.Prefix}}fleet_ensemble_plans ADD COLUMN selected_by BIGINT NULL;
ALTER TABLE {{.Prefix}}fleet_ensemble_plans ADD COLUMN selected_at DATETIME NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_ensemble_plans DROP COLUMN selected_at;
ALTER TABLE {{.Prefix}}fleet_ensemble_plans DROP COLUMN selected_by;
ALTER TABLE {{.Prefix}}fleet_ensemble_plans DROP COLUMN approved_at;
ALTER TABLE {{.Prefix}}fleet_ensemble_plans DROP COLUMN approved_by;
ALTER TABLE {{.Prefix}}fleet_ensemble_plans DROP COLUMN approval_requested_at;
