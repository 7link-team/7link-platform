-- +goose Up
ALTER TABLE {{.Prefix}}fleet_model_prices ADD COLUMN cache_read_micros BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}fleet_model_prices ADD COLUMN cache_write_micros BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}fleet_model_prices ADD COLUMN version INT NOT NULL DEFAULT 1;
ALTER TABLE {{.Prefix}}fleet_model_prices ADD COLUMN deleted_at DATETIME(3) NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_model_prices DROP COLUMN deleted_at;
ALTER TABLE {{.Prefix}}fleet_model_prices DROP COLUMN version;
ALTER TABLE {{.Prefix}}fleet_model_prices DROP COLUMN cache_write_micros;
ALTER TABLE {{.Prefix}}fleet_model_prices DROP COLUMN cache_read_micros;
