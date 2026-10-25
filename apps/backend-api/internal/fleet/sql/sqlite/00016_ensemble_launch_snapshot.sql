-- +goose Up
ALTER TABLE {{.Prefix}}fleet_ensemble_plans ADD COLUMN branch_snapshot TEXT NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_ensemble_plans DROP COLUMN branch_snapshot;
