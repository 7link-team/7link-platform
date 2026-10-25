-- +goose Up
CREATE TABLE {{.Prefix}}planning_loop_contracts (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT,
 agent_id BIGINT NOT NULL,
 owner_principal_id BIGINT NOT NULL,
 kind VARCHAR(32) NOT NULL,
 name VARCHAR(200) NOT NULL,
 anchor TEXT NOT NULL,
 schedule_kind VARCHAR(32) NOT NULL,
 interval_minutes INT,
 period_kind VARCHAR(32),
 report_binding TEXT,
 self_checks TEXT NOT NULL,
 budget TEXT NOT NULL,
 escalation TEXT NOT NULL,
 output_format VARCHAR(32) NOT NULL,
 allow_overlap BOOLEAN NOT NULL,
 state VARCHAR(32) NOT NULL,
 reminder_id BIGINT,
 reminder_fire_offset INT NOT NULL DEFAULT 0,
 consecutive_failures INT NOT NULL,
 version INT NOT NULL,
 created_by BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_by BIGINT NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CHECK (kind IN ('REPORT','CHECK')),
 CHECK (state IN ('DRAFT','ACTIVE','PAUSED','ARCHIVED')),
 CHECK (schedule_kind IN ('INTERVAL','PERIOD')),
 CHECK (version >= 1 AND consecutive_failures >= 0),
 CHECK (interval_minutes IS NULL OR interval_minutes BETWEEN 15 AND 10080)
);
CREATE TABLE {{.Prefix}}planning_loop_runs (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 loop_contract_id BIGINT NOT NULL,
 fire_no INT NOT NULL,
 scheduled_for TIMESTAMPTZ(3) NOT NULL,
 state VARCHAR(32) NOT NULL,
 report_id BIGINT,
 check_result TEXT,
 self_check_results TEXT NOT NULL,
 cost_micros BIGINT NOT NULL,
 opened_at TIMESTAMPTZ(3) NOT NULL,
 completed_at TIMESTAMPTZ(3),
 escalation_reason VARCHAR(32),
 note TEXT,
 PRIMARY KEY (tenant_id,id),
 UNIQUE (tenant_id,loop_contract_id,fire_no),
 CHECK (state IN ('OPEN','SUCCEEDED','FAILED','ESCALATED','SKIPPED','TIMED_OUT')),
 CHECK (fire_no > 0 AND cost_micros >= 0)
);
CREATE INDEX {{.Prefix}}pl_loop_agent ON {{.Prefix}}planning_loop_contracts (tenant_id,agent_id,state);
CREATE INDEX {{.Prefix}}pl_loop_project ON {{.Prefix}}planning_loop_contracts (tenant_id,project_id,state);
CREATE UNIQUE INDEX {{.Prefix}}pl_loop_reminder ON {{.Prefix}}planning_loop_contracts (tenant_id,reminder_id);
CREATE INDEX {{.Prefix}}pl_run_contract ON {{.Prefix}}planning_loop_runs (loop_contract_id,opened_at);
CREATE INDEX {{.Prefix}}pl_run_state ON {{.Prefix}}planning_loop_runs (state,opened_at);
-- +goose Down
DROP TABLE {{.Prefix}}planning_loop_runs;
DROP TABLE {{.Prefix}}planning_loop_contracts;
