-- +goose Up
CREATE TABLE {{.Prefix}}delivery_confirmation_requests (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 package_id BIGINT NOT NULL, source_version INT NOT NULL, active_key VARCHAR(8), requested_by BIGINT NOT NULL,
 requested_at DATETIME NOT NULL, withdrawn_at DATETIME, withdrawn_by BIGINT,
 version INT NOT NULL,
 CONSTRAINT {{.Prefix}}uq_delivery_confirmation_source UNIQUE (tenant_id,project_id,package_id,source_version,active_key),
 CONSTRAINT {{.Prefix}}ck_delivery_confirmation CHECK (id>0 AND tenant_id>0 AND project_id>0 AND package_id>0 AND source_version>0 AND requested_by>0 AND version>0),
 CONSTRAINT {{.Prefix}}ck_delivery_confirmation_withdrawal CHECK ((withdrawn_at IS NULL AND withdrawn_by IS NULL AND active_key IS NOT NULL AND active_key='ACTIVE') OR (withdrawn_at IS NOT NULL AND withdrawn_by IS NOT NULL AND withdrawn_by>0 AND active_key IS NULL)),
 CONSTRAINT {{.Prefix}}fk_delivery_confirmation_project FOREIGN KEY (tenant_id,project_id) REFERENCES {{.Prefix}}delivery_projects(tenant_id,project_id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}delivery_confirmation_pending ON {{.Prefix}}delivery_confirmation_requests(tenant_id,project_id,withdrawn_at,requested_at,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_confirmation_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_confirmation_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_confirmation_requests;
DROP TABLE {{.Prefix}}delivery_confirmation_rollback_guard;
DROP TABLE {{.Prefix}}delivery_confirmation_requests;
