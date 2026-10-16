-- +goose Up
CREATE TABLE {{.Prefix}}background_jobs (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL DEFAULT 0,
 queue TEXT COLLATE BINARY NOT NULL DEFAULT 'default',
 kind TEXT COLLATE BINARY NOT NULL,
 state TEXT COLLATE BINARY NOT NULL DEFAULT 'PENDING',
 priority SMALLINT NOT NULL DEFAULT 5,
 payload TEXT NOT NULL,
 idempotency_key TEXT COLLATE BINARY NULL,
 concurrency_key TEXT COLLATE BINARY NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 max_attempts INTEGER NOT NULL DEFAULT 8,
 run_at_ms BIGINT NOT NULL,
 lease_owner TEXT COLLATE BINARY NULL,
 lease_token BIGINT NOT NULL DEFAULT 0,
 lease_expires_at_ms BIGINT NULL,
 timeout_ms BIGINT NOT NULL DEFAULT 300000,
 last_error_code TEXT COLLATE BINARY NOT NULL DEFAULT '',
 last_error TEXT COLLATE BINARY NOT NULL DEFAULT '',
 error_history TEXT NOT NULL,
 manual_retries INTEGER NOT NULL DEFAULT 0,
 correlation_id TEXT COLLATE BINARY NOT NULL DEFAULT '',
 created_by BIGINT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 started_at DATETIME NULL,
 finished_at DATETIME NULL,
 version INTEGER NOT NULL DEFAULT 1,
 CHECK (length(queue) <= 64 AND length(kind) <= 128 AND length(state) <= 16 AND length(idempotency_key) <= 128 AND length(concurrency_key) <= 128 AND length(lease_owner) <= 64 AND length(last_error_code) <= 64 AND length(last_error) <= 1024 AND length(correlation_id) <= 64),
 CHECK (json_valid(payload)),
 CHECK (json_valid(error_history)),
 CONSTRAINT {{.Prefix}}uq_background_jobs_idempotency UNIQUE (tenant_id,kind,idempotency_key),
 CONSTRAINT {{.Prefix}}chk_jobs_state CHECK (state IN ('PENDING','RUNNING','SUCCEEDED','DEAD','CANCELLED')),
 CONSTRAINT {{.Prefix}}chk_jobs_priority CHECK (priority BETWEEN 1 AND 9),
 CONSTRAINT {{.Prefix}}chk_jobs_attempts CHECK (attempts >= 0 AND max_attempts BETWEEN 1 AND 50),
 CONSTRAINT {{.Prefix}}chk_jobs_timeout CHECK (timeout_ms BETWEEN 1000 AND 3600000)
);
CREATE INDEX {{.Prefix}}idx_background_jobs_claim ON {{.Prefix}}background_jobs (queue,state,priority,run_at_ms,id);
CREATE INDEX {{.Prefix}}idx_background_jobs_lease ON {{.Prefix}}background_jobs (state,lease_expires_at_ms);
CREATE INDEX {{.Prefix}}idx_background_jobs_tenant ON {{.Prefix}}background_jobs (queue,state,tenant_id);
CREATE INDEX {{.Prefix}}idx_background_jobs_pending_tenant ON {{.Prefix}}background_jobs (tenant_id,state,id);
CREATE INDEX {{.Prefix}}idx_background_jobs_concurrency ON {{.Prefix}}background_jobs (kind,concurrency_key,state);
CREATE INDEX {{.Prefix}}idx_background_jobs_admin ON {{.Prefix}}background_jobs (state,kind,created_at,id);
CREATE INDEX {{.Prefix}}idx_background_jobs_purge ON {{.Prefix}}background_jobs (state,finished_at,id);
CREATE TABLE {{.Prefix}}background_job_schedules (
 id BIGINT NOT NULL PRIMARY KEY,
 name TEXT COLLATE BINARY NOT NULL,
 kind TEXT COLLATE BINARY NOT NULL,
 queue TEXT COLLATE BINARY NOT NULL DEFAULT 'default',
 tenant_scope TEXT COLLATE BINARY NOT NULL DEFAULT 'PLATFORM',
 interval_ms BIGINT NULL,
 cron TEXT COLLATE BINARY NULL,
 timezone TEXT COLLATE BINARY NOT NULL DEFAULT 'UTC',
 payload TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT true,
 misfire_policy TEXT COLLATE BINARY NOT NULL DEFAULT 'COALESCE',
 next_run_at_ms BIGINT NOT NULL,
 last_fired_at_ms BIGINT NULL,
 last_job_id BIGINT NULL,
 definition_hash TEXT COLLATE BINARY NOT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 CHECK (length(name) <= 128 AND length(kind) <= 128 AND length(queue) <= 64 AND length(tenant_scope) <= 16 AND length(cron) <= 128 AND length(timezone) <= 64 AND length(misfire_policy) <= 16 AND length(definition_hash) <= 64),
 CHECK (json_valid(payload)),
 CHECK (enabled IN (0,1)),
 CONSTRAINT {{.Prefix}}uq_job_schedules_name UNIQUE (name),
 CONSTRAINT {{.Prefix}}chk_job_schedules_timing CHECK ((interval_ms IS NOT NULL AND interval_ms >= 1000 AND cron IS NULL) OR (interval_ms IS NULL AND cron IS NOT NULL)),
 CONSTRAINT {{.Prefix}}chk_job_schedules_scope CHECK (tenant_scope IN ('PLATFORM','PER_TENANT')),
 CONSTRAINT {{.Prefix}}chk_job_schedules_misfire CHECK (misfire_policy IN ('COALESCE','SKIP'))
);
CREATE INDEX {{.Prefix}}idx_background_job_schedules_due ON {{.Prefix}}background_job_schedules (enabled,next_run_at_ms);
-- +goose Down
DROP TABLE {{.Prefix}}background_job_schedules;
DROP TABLE {{.Prefix}}background_jobs;
