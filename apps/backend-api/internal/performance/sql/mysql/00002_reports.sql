-- +goose Up
CREATE TABLE {{.Prefix}}performance_reports (
 id BIGINT PRIMARY KEY NOT NULL, tenant_id BIGINT NOT NULL, scheme_id BIGINT NOT NULL,
 subject_kind VARCHAR(16) NOT NULL, subject_id BIGINT NOT NULL,
 cadence VARCHAR(16) NOT NULL, period_start VARCHAR(10) NOT NULL, period_end VARCHAR(10) NOT NULL,
 version INTEGER NOT NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}perf_report_tenant_uq UNIQUE (tenant_id,id),
 CONSTRAINT {{.Prefix}}perf_report_period_uq UNIQUE (tenant_id,scheme_id,subject_kind,subject_id,cadence,period_start,period_end),
 CONSTRAINT {{.Prefix}}perf_report_scheme_fk FOREIGN KEY (tenant_id,scheme_id) REFERENCES {{.Prefix}}performance_schemes(tenant_id,id),
 CONSTRAINT {{.Prefix}}perf_report_ver_ck CHECK (version>=0),
 CONSTRAINT {{.Prefix}}perf_report_subject_ck CHECK (subject_kind IN ('HUMAN','AGENT','PROJECT','TEAM')),
 CONSTRAINT {{.Prefix}}perf_report_cadence_ck CHECK (cadence IN ('WEEK','MONTH','QUARTER','YEAR')),
 CONSTRAINT {{.Prefix}}perf_report_dates_ck CHECK (period_start<=period_end)
);
CREATE TABLE {{.Prefix}}performance_report_revisions (
 id BIGINT PRIMARY KEY NOT NULL, tenant_id BIGINT NOT NULL, report_id BIGINT NOT NULL,
 version INTEGER NOT NULL, snapshot MEDIUMTEXT NOT NULL, sha256 VARCHAR(64) NOT NULL,
 reason VARCHAR(2000) NOT NULL, created_by BIGINT NOT NULL, created_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}perf_report_revision_uq UNIQUE (tenant_id,report_id,version),
 CONSTRAINT {{.Prefix}}perf_report_revision_fk FOREIGN KEY (tenant_id,report_id) REFERENCES {{.Prefix}}performance_reports(tenant_id,id),
 CONSTRAINT {{.Prefix}}perf_report_revision_ver_ck CHECK (version>0)
);
CREATE INDEX {{.Prefix}}perf_report_list_ix ON {{.Prefix}}performance_reports(tenant_id,period_start,cadence,id);
-- +goose Down
DROP TABLE {{.Prefix}}performance_report_revisions;
DROP TABLE {{.Prefix}}performance_reports;
