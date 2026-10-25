-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_embedding_configs ADD COLUMN active_provider_json TEXT NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE {{.Prefix}}knowledge_embedding_configs DROP COLUMN active_provider_json;
