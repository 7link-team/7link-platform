-- +goose Up
ALTER TABLE {{.Prefix}}fleet_terminal_access ADD COLUMN allow_remote_preview BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_terminal_access DROP COLUMN allow_remote_preview;
