-- +goose Up
ALTER TABLE {{.Prefix}}delivery_projects DROP CONSTRAINT {{.Prefix}}chk_delivery_project_archive;

-- +goose Down
ALTER TABLE {{.Prefix}}delivery_projects ADD CONSTRAINT {{.Prefix}}chk_delivery_project_archive CHECK (archived_at IS NULL OR stage IN ('CLOSED','CANCELLED'));
