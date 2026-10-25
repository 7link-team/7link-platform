-- +goose Up
CREATE TABLE {{.Prefix}}agents_operation_manifest_versions (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL DEFAULT 0,
 manifest_version VARCHAR(32) NOT NULL,
 sha256 VARCHAR(64) NOT NULL,
 document LONGTEXT NOT NULL,
 operation_count INT NOT NULL,
 supersedes_version VARCHAR(32) NOT NULL,
 published_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}agents_manifest_system CHECK (tenant_id = 0),
 CONSTRAINT {{.Prefix}}agents_manifest_version_unique UNIQUE (manifest_version)
);
-- +goose Down
DROP TABLE {{.Prefix}}agents_operation_manifest_versions;
