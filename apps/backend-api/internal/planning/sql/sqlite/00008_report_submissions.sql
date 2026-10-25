-- +goose Up
CREATE TABLE {{.Prefix}}planning_report_submissions (
 tenant_id INTEGER NOT NULL, id INTEGER NOT NULL, report_id INTEGER NOT NULL,
 report_version INT NOT NULL, submitted_at DATETIME NOT NULL, due_at DATETIME NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_report_submission UNIQUE (tenant_id,report_id,report_version),
 CONSTRAINT {{.Prefix}}ck_report_submission_version CHECK (report_version>1),
 CONSTRAINT {{.Prefix}}fk_report_submission FOREIGN KEY (tenant_id,report_id) REFERENCES {{.Prefix}}planning_reports (tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_report_submission_history ON {{.Prefix}}planning_report_submissions(tenant_id,report_id,submitted_at,id);
-- +goose Down
CREATE TABLE {{.Prefix}}planning_submission_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}planning_submission_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}planning_report_submissions;
DROP TABLE {{.Prefix}}planning_submission_rollback_guard;
DROP TABLE {{.Prefix}}planning_report_submissions;
