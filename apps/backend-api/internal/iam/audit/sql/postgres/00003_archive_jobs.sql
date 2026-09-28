-- Durable incremental archive jobs. Events remain append-only and are never
-- removed by this lifecycle. Lease times are database-clock Unix milliseconds.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_audit_archive_jobs (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    after_sequence BIGINT NOT NULL,
    head_sequence BIGINT NOT NULL,
    snapshot TEXT NOT NULL,
    state VARCHAR(16) COLLATE "C" NOT NULL,
    attempts BIGINT NOT NULL DEFAULT 0,
    lease_token BIGINT NOT NULL DEFAULT 0,
    lease_until BIGINT NOT NULL DEFAULT 0,
    next_attempt BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(32) NOT NULL DEFAULT '',
    object TEXT NOT NULL DEFAULT '{}',
    retain_until TIMESTAMPTZ(3) NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_archive_start UNIQUE (tenant_id, after_sequence),
    CONSTRAINT {{.Prefix}}chk_iam_archive_sequence CHECK (after_sequence >= 0 AND head_sequence > after_sequence),
    CONSTRAINT {{.Prefix}}chk_iam_archive_state CHECK (state IN ('queued', 'running', 'failed', 'complete')),
    CONSTRAINT {{.Prefix}}chk_iam_archive_attempts CHECK (attempts >= 0)
);
CREATE INDEX {{.Prefix}}idx_iam_archive_due ON {{.Prefix}}iam_audit_archive_jobs (state, next_attempt, lease_until);

-- +goose Down
DROP TABLE {{.Prefix}}iam_audit_archive_jobs;
