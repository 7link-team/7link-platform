-- +goose Up
ALTER TABLE {{.Prefix}}fleet_terminal_access ADD COLUMN tunnel_bandwidth_kbps INTEGER NOT NULL DEFAULT 20000;
ALTER TABLE {{.Prefix}}fleet_terminal_access ADD COLUMN tunnel_max_streams INTEGER NOT NULL DEFAULT 256;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_terminal_access DROP COLUMN tunnel_max_streams;
ALTER TABLE {{.Prefix}}fleet_terminal_access DROP COLUMN tunnel_bandwidth_kbps;
