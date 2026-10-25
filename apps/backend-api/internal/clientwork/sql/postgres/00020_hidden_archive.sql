-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_hidden_case_archive (
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 case_id BIGINT NOT NULL,
 case_version INT NOT NULL,
 content BYTEA NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 PRIMARY KEY (tenant_id, project_id, content_hash),
 CONSTRAINT {{.Prefix}}ck_hidden_archive_version CHECK (case_version > 0)
);
CREATE TABLE {{.Prefix}}clientwork_hidden_verification_receipts (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 result_id BIGINT NOT NULL,
 suite_hash VARCHAR(64) NOT NULL,
 head_commit VARCHAR(64) NOT NULL,
 verifier_principal_id BIGINT NOT NULL,
 model VARCHAR(200) NOT NULL,
 family VARCHAR(100) NOT NULL,
 verdict VARCHAR(32) NOT NULL,
 reason VARCHAR(64) NOT NULL,
 input_hash VARCHAR(64) NOT NULL,
 output_hash VARCHAR(64) NOT NULL,
 receipt_hash VARCHAR(64) NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_hidden_receipt_result UNIQUE (tenant_id,project_id,result_id,suite_hash),
 CONSTRAINT {{.Prefix}}ck_hidden_receipt_verdict CHECK (verdict IN ('PASSED','FAILED','INCONCLUSIVE','UNAVAILABLE'))
);
-- +goose Down
-- Frozen hidden bytes and verdict history must not be silently discarded.
CREATE TABLE IF NOT EXISTS {{.Prefix}}clientwork_hidden_archive_rollback_guard (allow_drop INT NOT NULL CHECK (allow_drop = 1));
INSERT INTO {{.Prefix}}clientwork_hidden_archive_rollback_guard (allow_drop) SELECT 0 FROM {{.Prefix}}clientwork_hidden_case_archive LIMIT 1;
INSERT INTO {{.Prefix}}clientwork_hidden_archive_rollback_guard (allow_drop) SELECT 0 FROM {{.Prefix}}clientwork_hidden_verification_receipts LIMIT 1;
DROP TABLE {{.Prefix}}clientwork_hidden_archive_rollback_guard;
DROP TABLE {{.Prefix}}clientwork_hidden_verification_receipts;
DROP TABLE {{.Prefix}}clientwork_hidden_case_archive;
