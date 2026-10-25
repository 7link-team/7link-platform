-- +goose Up
ALTER TABLE {{.Prefix}}templates_versions ADD COLUMN name VARCHAR(200) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}templates_versions ADD COLUMN origin VARCHAR(16) NOT NULL DEFAULT 'TEAM';
ALTER TABLE {{.Prefix}}templates_versions ADD COLUMN based_on_version_id BIGINT NULL;
ALTER TABLE {{.Prefix}}templates_versions ADD COLUMN based_on_sha256 VARCHAR(64) NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE {{.Prefix}}templates_versions DROP COLUMN based_on_sha256;
ALTER TABLE {{.Prefix}}templates_versions DROP COLUMN based_on_version_id;
ALTER TABLE {{.Prefix}}templates_versions DROP COLUMN origin;
ALTER TABLE {{.Prefix}}templates_versions DROP COLUMN name;
