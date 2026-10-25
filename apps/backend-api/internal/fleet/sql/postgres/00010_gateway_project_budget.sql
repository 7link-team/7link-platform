-- +goose Up
ALTER TABLE {{.Prefix}}fleet_gateway_requests ADD COLUMN project_id BIGINT NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_gateway_requests DROP COLUMN project_id;
