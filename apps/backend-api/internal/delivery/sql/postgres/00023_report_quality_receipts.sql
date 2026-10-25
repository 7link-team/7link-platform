-- +goose Up
CREATE TABLE {{.Prefix}}delivery_report_quality_observation (
 singleton INT NOT NULL PRIMARY KEY CHECK(singleton=1), observed_since TIMESTAMPTZ(3) NOT NULL
);
INSERT INTO {{.Prefix}}delivery_report_quality_observation(singleton,observed_since) VALUES (1,CURRENT_TIMESTAMP);
CREATE TABLE {{.Prefix}}delivery_report_quality_receipts (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL,
 kind VARCHAR(24) NOT NULL, source_id BIGINT NOT NULL, rounds INT NOT NULL, occurred_at TIMESTAMPTZ(3) NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_report_quality_source UNIQUE(tenant_id,work_item_id,kind,source_id),
 CONSTRAINT {{.Prefix}}ck_report_quality_kind CHECK(kind IN ('L3_ENTERED','INTERNAL_QA_PASSED','ESCAPE_HUMAN','ESCAPE_CLIENT')),
 CONSTRAINT {{.Prefix}}ck_report_quality_rounds CHECK((kind='INTERNAL_QA_PASSED' AND rounds>0) OR (kind<>'INTERNAL_QA_PASSED' AND rounds=0)),
 CONSTRAINT {{.Prefix}}fk_report_quality_work FOREIGN KEY(tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items(tenant_id,project_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_report_quality_period ON {{.Prefix}}delivery_report_quality_receipts(tenant_id,project_id,occurred_at,work_item_id);
-- +goose Down
CREATE TABLE {{.Prefix}}report_quality_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}report_quality_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_report_quality_receipts;
DROP TABLE {{.Prefix}}report_quality_rollback_guard;
DROP TABLE {{.Prefix}}delivery_report_quality_receipts;
DROP TABLE {{.Prefix}}delivery_report_quality_observation;
