-- +goose Up
CREATE TABLE {{.Prefix}}templates_versions (
 id INTEGER PRIMARY KEY NOT NULL, tenant_id INTEGER NOT NULL,
 template_key VARCHAR(64) NOT NULL, semver VARCHAR(16) NOT NULL,
 state VARCHAR(32) NOT NULL, definition TEXT NOT NULL,
 definition_sha256 VARCHAR(64) NOT NULL, version INTEGER NOT NULL,
 created_by INTEGER NOT NULL, updated_by INTEGER NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}tpl_ver_uq UNIQUE (tenant_id,template_key,semver),
 CONSTRAINT {{.Prefix}}tpl_state_ck CHECK (state IN ('DRAFT','PUBLISHED','DEPRECATED','RETIRED'))
);
CREATE TABLE {{.Prefix}}templates_project_bindings (
 id INTEGER PRIMARY KEY NOT NULL, tenant_id INTEGER NOT NULL, project_id INTEGER NOT NULL,
 template_version_id INTEGER NOT NULL, definition_sha256 VARCHAR(64) NOT NULL,
 constitution_id INTEGER NOT NULL, runs TEXT NOT NULL,
 created_by INTEGER NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}tpl_bind_uq UNIQUE (tenant_id,project_id),
 CONSTRAINT {{.Prefix}}tpl_bind_fk FOREIGN KEY (template_version_id) REFERENCES {{.Prefix}}templates_versions(id)
);
-- +goose Down
DROP TABLE {{.Prefix}}templates_project_bindings;
DROP TABLE {{.Prefix}}templates_versions;
