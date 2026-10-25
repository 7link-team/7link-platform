-- +goose Up
CREATE TABLE {{.Prefix}}planning_report_checks (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, report_id BIGINT NOT NULL,
 report_version INT NOT NULL, result VARCHAR(20) NOT NULL, checked_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}ck_report_check_version CHECK (report_version>0),
 CONSTRAINT {{.Prefix}}ck_report_check_result CHECK (result IN ('PASS','PASS_WITH_FLAGS','BLOCKED')),
 CONSTRAINT {{.Prefix}}fk_report_check FOREIGN KEY (tenant_id,report_id) REFERENCES {{.Prefix}}planning_reports (tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_report_check_history ON {{.Prefix}}planning_report_checks(tenant_id,report_id,report_version,checked_at,id);
-- +goose Down
CREATE TABLE {{.Prefix}}planning_check_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}planning_check_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}planning_report_checks;
DROP TABLE {{.Prefix}}planning_check_rollback_guard;
DROP TABLE {{.Prefix}}planning_report_checks;
