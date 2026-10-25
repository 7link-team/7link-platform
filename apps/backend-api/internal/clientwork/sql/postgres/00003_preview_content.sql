-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_previews ADD COLUMN binding_id BIGINT NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_previews DROP COLUMN binding_id;
