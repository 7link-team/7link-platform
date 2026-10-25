-- +goose Up
CREATE TABLE {{.Prefix}}templates_lifecycle_receipts (
 id BIGINT PRIMARY KEY NOT NULL,
 tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 template_version_id BIGINT NOT NULL,
 object_key VARCHAR(64) NOT NULL, source_key VARCHAR(128) NOT NULL,
 event_type VARCHAR(64) NOT NULL, state VARCHAR(24) NOT NULL,
 payload TEXT NOT NULL, request_hash VARCHAR(64) NOT NULL,
 created_by BIGINT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}tpl_lifecycle_source_uq UNIQUE (tenant_id, source_key),
 CONSTRAINT {{.Prefix}}tpl_lifecycle_version_fk FOREIGN KEY (template_version_id) REFERENCES {{.Prefix}}templates_versions(id)
);
CREATE INDEX {{.Prefix}}tpl_lifecycle_current_ix ON {{.Prefix}}templates_lifecycle_receipts(tenant_id,object_key,id);
-- +goose Down
DROP TABLE {{.Prefix}}templates_lifecycle_receipts;
