-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_chunks ADD COLUMN search_tokens TEXT NOT NULL DEFAULT '';
-- Native FTS5 is installed by the optional capability probe, because some
-- supported SQLite distributions do not include FTS5.
-- +goose Down
ALTER TABLE {{.Prefix}}knowledge_chunks DROP COLUMN search_tokens;
