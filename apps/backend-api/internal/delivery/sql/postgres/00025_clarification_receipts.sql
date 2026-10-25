-- +goose Up
CREATE TABLE {{.Prefix}}delivery_clarification_receipts (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 clarification_id BIGINT NOT NULL, reply_version INT NOT NULL, handled_by BIGINT NOT NULL,
 handled_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_clarification_receipt UNIQUE (tenant_id,project_id,clarification_id,reply_version),
 CONSTRAINT {{.Prefix}}ck_clarification_receipt CHECK (id>0 AND tenant_id>0 AND project_id>0 AND clarification_id>0 AND reply_version>0 AND handled_by>0),
 CONSTRAINT {{.Prefix}}fk_clarification_receipt_project FOREIGN KEY (tenant_id,project_id) REFERENCES {{.Prefix}}delivery_projects(tenant_id,project_id) ON DELETE RESTRICT
);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_clarification_receipts;
