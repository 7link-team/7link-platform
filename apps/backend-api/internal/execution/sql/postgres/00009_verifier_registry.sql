-- +goose Up
CREATE TABLE {{.Prefix}}execution_verifiers (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL,
 "key" VARCHAR(64) NOT NULL, name VARCHAR(200) NOT NULL,
 layer VARCHAR(16) NOT NULL, state VARCHAR(16) NOT NULL,
 current_version_no INT NOT NULL, applies_to TEXT NOT NULL,
 required BOOLEAN NOT NULL, version INT NOT NULL, created_by BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_verifier_tenant UNIQUE(tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_verifier_key UNIQUE(tenant_id,"key"),
 CONSTRAINT {{.Prefix}}ck_verifier_layer CHECK(layer IN ('AUTOMATED','AI_ASSISTED','HUMAN')),
 CONSTRAINT {{.Prefix}}ck_verifier_state CHECK(state IN ('ACTIVE','ARCHIVED')),
 CONSTRAINT {{.Prefix}}ck_verifier_versions CHECK(current_version_no>0 AND version>0)
);
CREATE INDEX {{.Prefix}}idx_verifier_selection ON {{.Prefix}}execution_verifiers(tenant_id,state,id);
CREATE TABLE {{.Prefix}}execution_verifier_versions (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, verifier_id BIGINT NOT NULL,
 version_no INT NOT NULL, spec TEXT NOT NULL, timeout_ms BIGINT NOT NULL,
 change_note VARCHAR(2000) NOT NULL, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_verifier_version UNIQUE(tenant_id,verifier_id,version_no),
 CONSTRAINT {{.Prefix}}fk_verifier_version FOREIGN KEY(tenant_id,verifier_id) REFERENCES {{.Prefix}}execution_verifiers(tenant_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}ck_verifier_version CHECK(version_no>0 AND timeout_ms BETWEEN 1000 AND 3600000)
);
-- +goose Down
DROP TABLE {{.Prefix}}execution_verifier_versions;
DROP TABLE {{.Prefix}}execution_verifiers;
