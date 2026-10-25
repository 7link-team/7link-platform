-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_tolerances ADD COLUMN forecast_measurement TEXT NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_tolerances DROP COLUMN forecast_measurement;
