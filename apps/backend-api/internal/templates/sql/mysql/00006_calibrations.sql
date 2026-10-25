-- +goose Up
CREATE UNIQUE INDEX {{.Prefix}}uq_cal_version_tenant ON {{.Prefix}}templates_versions(tenant_id,id);
CREATE TABLE {{.Prefix}}templates_calibration_samples (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, template_version_id BIGINT NOT NULL,
 verifier_key VARCHAR(64) NOT NULL, origin VARCHAR(16) NOT NULL,
 input LONGTEXT NOT NULL, expected VARCHAR(16) NOT NULL, sha256 VARCHAR(64) NOT NULL,
 created_by BIGINT NOT NULL, created_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_cal_sample_input UNIQUE(tenant_id,template_version_id,verifier_key,sha256),
 CONSTRAINT {{.Prefix}}ck_cal_sample_origin CHECK(origin IN ('MANUAL','ESCAPE')),
 CONSTRAINT {{.Prefix}}ck_cal_sample_label CHECK(expected IN ('PASS','WARN','FAIL')),
 CONSTRAINT {{.Prefix}}fk_cal_sample_ver FOREIGN KEY(tenant_id,template_version_id) REFERENCES {{.Prefix}}templates_versions(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}ix_cal_samples_ver ON {{.Prefix}}templates_calibration_samples(tenant_id,template_version_id,verifier_key,id);
CREATE TABLE {{.Prefix}}templates_verifier_calibrations (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, template_version_id BIGINT NOT NULL,
 template_key VARCHAR(64) NOT NULL, verifier_key VARCHAR(64) NOT NULL, provider_ref VARCHAR(120) NOT NULL,
 method VARCHAR(16) NOT NULL, machine_id BIGINT NOT NULL, state VARCHAR(16) NOT NULL,
 current_run_id BIGINT NOT NULL, target_ppm INTEGER NOT NULL,
 calibrated_at DATETIME(3) NULL, calibrated_by BIGINT NULL, drift_checked_at DATETIME(3) NULL,
 next_review_at DATETIME(3) NOT NULL, created_by BIGINT NOT NULL, created_at DATETIME(3) NOT NULL, version INTEGER NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_cal_target UNIQUE(tenant_id,template_version_id,verifier_key,provider_ref,machine_id),
 CONSTRAINT {{.Prefix}}ck_cal_state CHECK(state IN ('UNCALIBRATED','CALIBRATING','CALIBRATED','DRIFTED')),
 CONSTRAINT {{.Prefix}}ck_cal_method CHECK((method='AGREEMENT' AND machine_id=0) OR (method='PROBE' AND machine_id>0)),
 CONSTRAINT {{.Prefix}}ck_cal_target CHECK(target_ppm BETWEEN 0 AND 1000000 AND version>0),
 CONSTRAINT {{.Prefix}}fk_cal_ver FOREIGN KEY(tenant_id,template_version_id) REFERENCES {{.Prefix}}templates_versions(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}ix_cal_due ON {{.Prefix}}templates_verifier_calibrations(tenant_id,next_review_at,id);
CREATE TABLE {{.Prefix}}templates_calibration_runs (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, calibration_id BIGINT NOT NULL,
 state VARCHAR(16) NOT NULL, snapshot LONGTEXT NOT NULL, sha256 VARCHAR(64) NOT NULL,
 results LONGTEXT NOT NULL, agreement_ppm INTEGER NULL, sample_size INTEGER NOT NULL,
 escape_count INTEGER NOT NULL, recheck BOOLEAN NOT NULL, limitations VARCHAR(2000) NOT NULL,
 created_at DATETIME(3) NOT NULL, completed_at DATETIME(3) NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}ck_cal_run_state CHECK(state IN ('PENDING','COMPLETED','UNAVAILABLE','FAILED')),
 CONSTRAINT {{.Prefix}}ck_cal_agreement CHECK(agreement_ppm IS NULL OR agreement_ppm BETWEEN 0 AND 1000000),
 CONSTRAINT {{.Prefix}}fk_cal_run_head FOREIGN KEY(tenant_id,calibration_id) REFERENCES {{.Prefix}}templates_verifier_calibrations(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}ix_cal_run_pending ON {{.Prefix}}templates_calibration_runs(tenant_id,state,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}calibration_rollback_guard(n INTEGER NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}calibration_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}templates_calibration_samples;
INSERT INTO {{.Prefix}}calibration_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}templates_verifier_calibrations;
DROP TABLE {{.Prefix}}calibration_rollback_guard;
DROP TABLE {{.Prefix}}templates_calibration_runs;
DROP TABLE {{.Prefix}}templates_verifier_calibrations;
DROP TABLE {{.Prefix}}templates_calibration_samples;
DROP INDEX {{.Prefix}}uq_cal_version_tenant ON {{.Prefix}}templates_versions;
