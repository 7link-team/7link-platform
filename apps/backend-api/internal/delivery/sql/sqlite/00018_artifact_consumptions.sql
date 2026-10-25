-- +goose Up
CREATE TABLE {{.Prefix}}delivery_artifact_consumptions (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL, upstream_work_item_id BIGINT NOT NULL,
 accepted_artifact_id BIGINT NOT NULL, result_id BIGINT NOT NULL, file_id BIGINT NOT NULL, version_id BIGINT NOT NULL,
 content_sha256 VARCHAR(64) NOT NULL, state VARCHAR(16) NOT NULL DEFAULT 'CURRENT', observed_hash VARCHAR(64) NOT NULL DEFAULT '',
 created_by BIGINT NOT NULL, created_at DATETIME NOT NULL, checked_at DATETIME NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}artifact_consumption_unique UNIQUE (tenant_id,work_item_id,accepted_artifact_id),
 CONSTRAINT {{.Prefix}}artifact_consumption_work_fk FOREIGN KEY (tenant_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items(tenant_id,id),
 CONSTRAINT {{.Prefix}}artifact_consumption_source_fk FOREIGN KEY (tenant_id,upstream_work_item_id) REFERENCES {{.Prefix}}delivery_work_items(tenant_id,id),
 CONSTRAINT {{.Prefix}}artifact_consumption_state CHECK (state IN ('CURRENT','STALE','INCONCLUSIVE','SUPERSEDED'))
);
CREATE INDEX {{.Prefix}}artifact_consumption_project ON {{.Prefix}}delivery_artifact_consumptions(tenant_id,project_id,work_item_id);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_artifact_consumptions;
