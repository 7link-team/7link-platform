-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_sources ADD COLUMN tags_json VARCHAR(2048) NOT NULL DEFAULT '[]';
ALTER TABLE {{.Prefix}}knowledge_sources ADD COLUMN archived_at TIMESTAMPTZ(3) NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}knowledge_sources DROP COLUMN archived_at;
ALTER TABLE {{.Prefix}}knowledge_sources DROP COLUMN tags_json;
