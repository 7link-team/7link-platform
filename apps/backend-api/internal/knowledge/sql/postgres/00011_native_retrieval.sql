-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_chunks ADD COLUMN search_tokens TEXT NOT NULL DEFAULT '';
-- The optional native adapter installs its GIN index after capability probing.
-- +goose Down
ALTER TABLE {{.Prefix}}knowledge_chunks DROP COLUMN search_tokens;
