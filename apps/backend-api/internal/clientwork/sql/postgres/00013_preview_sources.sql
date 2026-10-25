-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_previews ADD COLUMN source_file_id BIGINT NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_previews DROP COLUMN source_file_id;
