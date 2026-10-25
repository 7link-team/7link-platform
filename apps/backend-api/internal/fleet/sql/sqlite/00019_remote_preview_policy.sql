-- +goose Up
ALTER TABLE {{.Prefix}}fleet_terminal_access ADD COLUMN allow_remote_preview INTEGER NOT NULL DEFAULT 0;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_terminal_access DROP COLUMN allow_remote_preview;
