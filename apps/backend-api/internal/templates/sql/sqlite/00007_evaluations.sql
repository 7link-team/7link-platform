-- +goose Up
CREATE TABLE {{.Prefix}}templates_eval_cases (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, template_version_id BIGINT NOT NULL,
 template_key VARCHAR(64) NOT NULL, project_id BIGINT NOT NULL,
 origin VARCHAR(16) NOT NULL, kind VARCHAR(16) NOT NULL, title VARCHAR(200) NOT NULL,
 input_ref TEXT NOT NULL, expected TEXT NOT NULL, source_escape_id BIGINT NULL,
 source_snapshot TEXT NOT NULL, active BOOLEAN NOT NULL, version INTEGER NOT NULL,
 created_by BIGINT NOT NULL, created_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_eval_escape UNIQUE(tenant_id,template_key,source_escape_id),
 CONSTRAINT {{.Prefix}}ck_eval_origin CHECK(origin IN ('SEED','MANUAL','ESCAPE')),
 CONSTRAINT {{.Prefix}}ck_eval_kind CHECK(kind IN ('JUDGEMENT','TOOL_PROBE')),
 CONSTRAINT {{.Prefix}}ck_eval_escape CHECK((origin='ESCAPE' AND source_escape_id IS NOT NULL) OR (origin<>'ESCAPE' AND source_escape_id IS NULL)),
 CONSTRAINT {{.Prefix}}ck_eval_case_version CHECK(version>0),
 CONSTRAINT {{.Prefix}}fk_eval_case_version FOREIGN KEY(tenant_id,template_version_id) REFERENCES {{.Prefix}}templates_versions(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}ix_eval_case_list ON {{.Prefix}}templates_eval_cases(tenant_id,template_key,active,id);
CREATE TABLE {{.Prefix}}templates_eval_runs (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, template_version_id BIGINT NOT NULL,
 template_key VARCHAR(64) NOT NULL, target_key VARCHAR(64) NOT NULL,
 snapshot TEXT NOT NULL, sha256 VARCHAR(64) NOT NULL, state VARCHAR(16) NOT NULL,
 results TEXT NOT NULL, cases_total INTEGER NOT NULL, passed INTEGER NOT NULL,
 failed INTEGER NOT NULL, inconclusive INTEGER NOT NULL, agreement_ppm INTEGER NULL,
 limitations VARCHAR(2000) NOT NULL, created_by BIGINT NOT NULL, started_at DATETIME NOT NULL, finished_at DATETIME NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}ck_eval_state CHECK(state IN ('RUNNING','COMPLETED','UNAVAILABLE','FAILED')),
 CONSTRAINT {{.Prefix}}ck_eval_counts CHECK(cases_total>=0 AND passed>=0 AND failed>=0 AND inconclusive>=0 AND passed+failed+inconclusive<=cases_total),
 CONSTRAINT {{.Prefix}}ck_eval_agreement CHECK(agreement_ppm IS NULL OR agreement_ppm BETWEEN 0 AND 1000000),
 CONSTRAINT {{.Prefix}}fk_eval_run_version FOREIGN KEY(tenant_id,template_version_id) REFERENCES {{.Prefix}}templates_versions(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}ix_eval_run_pending ON {{.Prefix}}templates_eval_runs(tenant_id,state,id);
-- +goose Down
CREATE TEMPORARY TABLE {{.Prefix}}eval_rollback_guard(n INTEGER NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}eval_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}templates_eval_cases;
INSERT INTO {{.Prefix}}eval_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}templates_eval_runs;
DROP TABLE {{.Prefix}}eval_rollback_guard;
DROP TABLE {{.Prefix}}templates_eval_runs;
DROP TABLE {{.Prefix}}templates_eval_cases;
