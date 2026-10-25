-- +goose Up
ALTER TABLE {{.Prefix}}delivery_work_items ADD COLUMN reopened_from_work_item_id BIGINT NULL;
ALTER TABLE {{.Prefix}}delivery_work_items ADD COLUMN escaped_from_stage VARCHAR(32) NULL;
CREATE TABLE {{.Prefix}}delivery_deployments (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 result_id BIGINT NOT NULL,
 producer_id BIGINT NOT NULL,
 event_key VARCHAR(128) NOT NULL,
 environment VARCHAR(128) NOT NULL,
 commit_hash VARCHAR(64) NOT NULL,
 artifact_hash VARCHAR(64) NOT NULL,
 merge_receipt_hash VARCHAR(64) NOT NULL,
 fixed_version VARCHAR(64) NOT NULL,
 payload_hash VARCHAR(64) NOT NULL,
 occurred_at DATETIME NOT NULL,
 created_at DATETIME NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_deployment_event UNIQUE (tenant_id,project_id,producer_id,event_key),
 CONSTRAINT {{.Prefix}}fk_delivery_deployment_work FOREIGN KEY (tenant_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,id),
 CONSTRAINT {{.Prefix}}fk_delivery_deployment_result FOREIGN KEY (tenant_id,result_id) REFERENCES {{.Prefix}}delivery_result_versions (tenant_id,id),
 CHECK (tenant_id>0 AND id>0 AND project_id>0 AND work_item_id>0 AND result_id>0 AND producer_id>0),
 CHECK (length(artifact_hash)=64 AND length(merge_receipt_hash)=64 AND length(payload_hash)=64),
 CHECK (length(commit_hash) IN (40,64))
);
CREATE INDEX {{.Prefix}}idx_delivery_deployment_result ON {{.Prefix}}delivery_deployments (tenant_id,project_id,work_item_id,result_id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_lifecycle_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_lifecycle_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_deployments;
INSERT INTO {{.Prefix}}delivery_lifecycle_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_items WHERE reopened_from_work_item_id IS NOT NULL OR escaped_from_stage IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_lifecycle_rollback_guard;
DROP TABLE {{.Prefix}}delivery_deployments;
ALTER TABLE {{.Prefix}}delivery_work_items DROP COLUMN escaped_from_stage;
ALTER TABLE {{.Prefix}}delivery_work_items DROP COLUMN reopened_from_work_item_id;
