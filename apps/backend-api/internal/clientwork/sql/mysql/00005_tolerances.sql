-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_tolerances (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 subject_kind VARCHAR(16) NOT NULL CHECK (subject_kind IN ('PROJECT','REQUIREMENT')),
 subject_id BIGINT NOT NULL, appetite_end_at DATETIME(3) NOT NULL,
 appetite_cost_micros BIGINT NULL CHECK (appetite_cost_micros >= 0),
 appetite_scope_criteria BIGINT NULL CHECK (appetite_scope_criteria >= 0),
 tolerance_time_pct INTEGER NOT NULL CHECK (tolerance_time_pct BETWEEN 0 AND 100),
 tolerance_cost_pct INTEGER NOT NULL CHECK (tolerance_cost_pct BETWEEN 0 AND 100),
 tolerance_scope_pct INTEGER NOT NULL CHECK (tolerance_scope_pct BETWEEN 0 AND 100),
 forecast_end_at DATETIME(3) NULL, forecast_cost_micros BIGINT NULL,
 forecast_scope_criteria BIGINT NULL, forecast_at DATETIME(3) NULL,
 state VARCHAR(16) NOT NULL CHECK (state IN ('OK','AT_RISK','EXCEPTION')),
 open_exception_id BIGINT NULL, baseline_no INTEGER NOT NULL CHECK (baseline_no > 0),
 version INTEGER NOT NULL CHECK (version > 0), created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,subject_kind,subject_id)
);
CREATE INDEX {{.Prefix}}clientwork_tolerances_project ON {{.Prefix}}clientwork_tolerances (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_exceptions (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL, tolerance_id BIGINT NOT NULL,
 breached_dimensions TEXT NOT NULL, forecast_snapshot TEXT NOT NULL, report TEXT NULL,
 state VARCHAR(16) NOT NULL CHECK (state IN ('RAISED','REPORTED','DECIDED')),
 decision VARCHAR(16) NULL CHECK (decision IN ('CONTINUE','RESHAPE','HOLD','KILL')),
 decision_payload TEXT NULL, decided_by BIGINT NULL, decided_at DATETIME(3) NULL, decision_reason TEXT NULL,
 version INTEGER NOT NULL CHECK (version > 0), created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 FOREIGN KEY (tenant_id,tolerance_id) REFERENCES {{.Prefix}}clientwork_tolerances (tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_exceptions_project ON {{.Prefix}}clientwork_exceptions (tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_exceptions;
DROP TABLE {{.Prefix}}clientwork_tolerances;
