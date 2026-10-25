-- +goose Up
ALTER TABLE {{.Prefix}}fleet_gateway_keys ADD COLUMN revoked_at DATETIME NULL;
ALTER TABLE {{.Prefix}}fleet_gateway_keys ADD COLUMN replacement_id BIGINT NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_gateway_keys DROP COLUMN replacement_id;
ALTER TABLE {{.Prefix}}fleet_gateway_keys DROP COLUMN revoked_at;
