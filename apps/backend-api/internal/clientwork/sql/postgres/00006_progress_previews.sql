-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_previews ADD COLUMN source_stage VARCHAR(16) NOT NULL DEFAULT 'ACCEPTED' CHECK (source_stage IN ('ACCEPTED','PROGRESS'));
-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_previews DROP COLUMN source_stage;
