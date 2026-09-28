-- +goose Up
ALTER TABLE {{.Prefix}}file_operations ADD COLUMN plan_json TEXT NOT NULL DEFAULT '{}';
-- +goose Down
ALTER TABLE {{.Prefix}}file_operations DROP COLUMN plan_json;
