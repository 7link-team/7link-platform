-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_packages ADD COLUMN archived_at DATETIME(3) NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_packages DROP COLUMN archived_at;
