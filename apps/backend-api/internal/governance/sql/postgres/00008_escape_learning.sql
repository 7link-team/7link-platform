-- +goose Up
ALTER TABLE {{.Prefix}}governance_quality_escapes ADD COLUMN learning_draft TEXT;
-- +goose Down
ALTER TABLE {{.Prefix}}governance_quality_escapes DROP COLUMN learning_draft;
