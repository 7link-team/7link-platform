-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_chunks ADD COLUMN search_tokens MEDIUMTEXT NOT NULL DEFAULT ('');
-- ngram FULLTEXT is installed by the capability probe, never required by core.
-- +goose Down
ALTER TABLE {{.Prefix}}knowledge_chunks DROP COLUMN search_tokens;
