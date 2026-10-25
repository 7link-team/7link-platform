-- +goose Up
ALTER TABLE {{.Prefix}}templates_project_bindings ADD COLUMN version INT NOT NULL DEFAULT 1;
ALTER TABLE {{.Prefix}}templates_project_bindings ADD COLUMN brief_overrides VARCHAR(12000) NOT NULL DEFAULT '{}';
ALTER TABLE {{.Prefix}}templates_project_bindings ADD COLUMN overrides_approved_by BIGINT NULL;
ALTER TABLE {{.Prefix}}templates_project_bindings ADD COLUMN overrides_approved_at DATETIME(3) NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}templates_project_bindings DROP COLUMN overrides_approved_at;
ALTER TABLE {{.Prefix}}templates_project_bindings DROP COLUMN overrides_approved_by;
ALTER TABLE {{.Prefix}}templates_project_bindings DROP COLUMN brief_overrides;
ALTER TABLE {{.Prefix}}templates_project_bindings DROP COLUMN version;
