-- +goose Up
CREATE TABLE {{.Prefix}}templates_studio_records (
 id BIGINT PRIMARY KEY NOT NULL, tenant_id BIGINT NOT NULL, binding_id BIGINT NOT NULL,
 domain VARCHAR(16) NOT NULL, kind VARCHAR(32) NOT NULL,
 parent_id BIGINT, selected_id BIGINT, state VARCHAR(16) NOT NULL,
 payload TEXT NOT NULL, version INTEGER NOT NULL,
 created_by BIGINT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}tpl_studio_binding_fk FOREIGN KEY (binding_id) REFERENCES {{.Prefix}}templates_project_bindings(id),
 CONSTRAINT {{.Prefix}}tpl_studio_domain_ck CHECK (domain IN ('VIDEO','NOVEL','TEAM_TASKS')),
 CONSTRAINT {{.Prefix}}tpl_studio_kind_ck CHECK (kind IN ('SHOT','CANDIDATE','QC_NOTE','CHAPTER','CONTINUITY','FORESHADOW','ASSISTANT_DRAFT','TASK')),
 CONSTRAINT {{.Prefix}}tpl_studio_state_ck CHECK (state IN ('OPEN','CONFIRMED','REJECTED','RESOLVED')),
 CONSTRAINT {{.Prefix}}tpl_studio_version_ck CHECK (version > 0)
);
CREATE INDEX {{.Prefix}}tpl_studio_page_ix ON {{.Prefix}}templates_studio_records(tenant_id,binding_id,domain,id);
-- +goose Down
DROP TABLE {{.Prefix}}templates_studio_records;
