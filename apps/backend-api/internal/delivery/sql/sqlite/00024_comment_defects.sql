-- +goose Up
CREATE TABLE {{.Prefix}}delivery_comment_defects (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL, thread_id BIGINT NOT NULL, execution_id BIGINT NOT NULL,
 source_first_body TEXT NOT NULL, found_in_work_item_id BIGINT NOT NULL,
 anchor_snapshot TEXT NOT NULL, steps TEXT NOT NULL, expected TEXT NOT NULL, actual TEXT NOT NULL,
 found_stage VARCHAR(16) NOT NULL, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}uq_comment_defect_thread UNIQUE(tenant_id,thread_id),
 CONSTRAINT {{.Prefix}}uq_comment_defect_work UNIQUE(tenant_id,work_item_id),
 CONSTRAINT {{.Prefix}}fk_comment_defect_work FOREIGN KEY(tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items(tenant_id,project_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_comment_defect_source FOREIGN KEY(tenant_id,project_id,found_in_work_item_id) REFERENCES {{.Prefix}}delivery_work_items(tenant_id,project_id,id) ON DELETE RESTRICT
);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_comment_defects;
