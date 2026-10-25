-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT 'PORTAL';
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN recorded_by BIGINT;
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN evidence_binding_id BIGINT;
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN evidence_json TEXT;
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}acceptance_record_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}acceptance_record_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}clientwork_package_receipts WHERE source<>'PORTAL' OR recorded_by IS NOT NULL OR evidence_binding_id IS NOT NULL OR evidence_json IS NOT NULL;
DROP TABLE {{.Prefix}}acceptance_record_rollback_guard;
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN evidence_json;
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN evidence_binding_id;
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN recorded_by;
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN source;
