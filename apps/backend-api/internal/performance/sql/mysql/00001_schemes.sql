-- +goose Up
CREATE TABLE {{.Prefix}}performance_schemes (
 id BIGINT PRIMARY KEY NOT NULL, tenant_id BIGINT NOT NULL,
 version INTEGER NOT NULL, enabled BOOLEAN NOT NULL,
 created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}perf_scheme_tenant_uq UNIQUE (tenant_id,id),
 CONSTRAINT {{.Prefix}}perf_scheme_ver_ck CHECK (version>0)
);
CREATE TABLE {{.Prefix}}performance_scheme_revisions (
 id BIGINT PRIMARY KEY NOT NULL, tenant_id BIGINT NOT NULL, scheme_id BIGINT NOT NULL,
 version INTEGER NOT NULL, definition MEDIUMTEXT NOT NULL, sha256 VARCHAR(64) NOT NULL,
 reason VARCHAR(2000) NOT NULL, created_by BIGINT NOT NULL, created_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}perf_revision_uq UNIQUE (tenant_id,scheme_id,version),
 CONSTRAINT {{.Prefix}}perf_revision_scheme_fk FOREIGN KEY (tenant_id,scheme_id) REFERENCES {{.Prefix}}performance_schemes(tenant_id,id),
 CONSTRAINT {{.Prefix}}perf_revision_ver_ck CHECK (version>0)
);
CREATE INDEX {{.Prefix}}perf_scheme_list_ix ON {{.Prefix}}performance_schemes(tenant_id,enabled,id);
-- +goose Down
DROP TABLE {{.Prefix}}performance_scheme_revisions;
DROP TABLE {{.Prefix}}performance_schemes;
