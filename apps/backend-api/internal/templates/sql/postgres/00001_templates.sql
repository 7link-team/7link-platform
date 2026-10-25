-- +goose Up
CREATE TABLE {{.Prefix}}templates_versions (
 id BIGINT PRIMARY KEY NOT NULL, tenant_id BIGINT NOT NULL,
 template_key VARCHAR(64) NOT NULL, semver VARCHAR(16) NOT NULL,
 state VARCHAR(32) NOT NULL, definition TEXT NOT NULL,
 definition_sha256 VARCHAR(64) NOT NULL, version INTEGER NOT NULL,
 created_by BIGINT NOT NULL, updated_by BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}tpl_ver_uq UNIQUE (tenant_id,template_key,semver),
 CONSTRAINT {{.Prefix}}tpl_state_ck CHECK (state IN ('DRAFT','PUBLISHED','DEPRECATED','RETIRED'))
);
CREATE TABLE {{.Prefix}}templates_project_bindings (
 id BIGINT PRIMARY KEY NOT NULL, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 template_version_id BIGINT NOT NULL, definition_sha256 VARCHAR(64) NOT NULL,
 constitution_id BIGINT NOT NULL, runs TEXT NOT NULL,
 created_by BIGINT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}tpl_bind_uq UNIQUE (tenant_id,project_id),
 CONSTRAINT {{.Prefix}}tpl_bind_fk FOREIGN KEY (template_version_id) REFERENCES {{.Prefix}}templates_versions(id)
);
-- +goose Down
DROP TABLE {{.Prefix}}templates_project_bindings;
DROP TABLE {{.Prefix}}templates_versions;
