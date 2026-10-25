-- +goose Up
CREATE UNIQUE INDEX {{.Prefix}}uq_prov_binding_tenant ON {{.Prefix}}templates_project_bindings(tenant_id,id);
CREATE TABLE {{.Prefix}}templates_providers (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,kind VARCHAR(16) NOT NULL,adapter_key VARCHAR(64) NOT NULL,
 display_name VARCHAR(120) NOT NULL,config TEXT NOT NULL,sealed_secret TEXT NOT NULL,machine_id BIGINT NOT NULL,
 state VARCHAR(16) NOT NULL,license_sha256 VARCHAR(64) NOT NULL,license_accepted_by BIGINT NOT NULL,
 license_accepted_at TIMESTAMPTZ NOT NULL,health_checked_at TIMESTAMPTZ NULL,health_note VARCHAR(2000) NOT NULL,
 created_by BIGINT NOT NULL,version INTEGER NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}ck_tpl_provider_kind CHECK(kind IN ('VIDEO_GEN','IMAGE_GEN','TTS','MUSIC_GEN','ASR_ALIGN','VLM_JUDGE','LLM_JUDGE')),
 CONSTRAINT {{.Prefix}}ck_tpl_provider_state CHECK(state IN ('ENABLED','DISABLED','UNAVAILABLE')),
 CONSTRAINT {{.Prefix}}ck_tpl_provider_ver CHECK(version>0 AND machine_id>=0)
);
CREATE TABLE {{.Prefix}}templates_provider_bindings (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,binding_id BIGINT NOT NULL,project_id BIGINT NOT NULL,
 kind VARCHAR(16) NOT NULL,provider_id BIGINT NOT NULL,provider_version INTEGER NOT NULL,
 created_by BIGINT NOT NULL,created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}fk_tpl_prov_bind FOREIGN KEY(tenant_id,binding_id) REFERENCES {{.Prefix}}templates_project_bindings(tenant_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_tpl_prov_provider FOREIGN KEY(tenant_id,provider_id) REFERENCES {{.Prefix}}templates_providers(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}ix_tpl_prov_project ON {{.Prefix}}templates_provider_bindings(tenant_id,binding_id,kind,id);
CREATE TABLE {{.Prefix}}templates_provider_generations (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,binding_id BIGINT NOT NULL,
 provider_id BIGINT NOT NULL,provider_version INTEGER NOT NULL,execution_id BIGINT NOT NULL,
 kind VARCHAR(16) NOT NULL,adapter_key VARCHAR(64) NOT NULL,config_snapshot TEXT NOT NULL,
 secret_snapshot TEXT NOT NULL,machine_id BIGINT NOT NULL,request TEXT NOT NULL,request_sha256 VARCHAR(64) NOT NULL,
 job_ref VARCHAR(200) NOT NULL,state VARCHAR(24) NOT NULL,output_snapshot TEXT NOT NULL,
 output_sha256 VARCHAR(64) NOT NULL,created_at TIMESTAMPTZ NOT NULL,completed_at TIMESTAMPTZ NULL,version INTEGER NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_tpl_prov_execution UNIQUE(tenant_id,execution_id,kind),
 CONSTRAINT {{.Prefix}}ck_tpl_prov_gen_state CHECK(state IN ('PENDING','RUNNING','COMPLETED','FAILED','CANCEL_REQUESTED','CANCELLED')),
 CONSTRAINT {{.Prefix}}fk_tpl_gen_bind FOREIGN KEY(tenant_id,binding_id) REFERENCES {{.Prefix}}templates_project_bindings(tenant_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_tpl_gen_provider FOREIGN KEY(tenant_id,provider_id) REFERENCES {{.Prefix}}templates_providers(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}ix_tpl_prov_gen_pending ON {{.Prefix}}templates_provider_generations(tenant_id,state,id);
-- +goose Down
CREATE TEMPORARY TABLE {{.Prefix}}provider_rollback_guard(n INTEGER NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}provider_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}templates_providers;
INSERT INTO {{.Prefix}}provider_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}templates_provider_bindings;
INSERT INTO {{.Prefix}}provider_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}templates_provider_generations;
DROP TABLE {{.Prefix}}provider_rollback_guard;
DROP TABLE {{.Prefix}}templates_provider_generations;
DROP TABLE {{.Prefix}}templates_provider_bindings;
DROP TABLE {{.Prefix}}templates_providers;
DROP INDEX {{.Prefix}}uq_prov_binding_tenant;
