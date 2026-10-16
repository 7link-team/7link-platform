-- +goose Up
CREATE TABLE {{.Prefix}}domain_events (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id BIGINT NULL,
    event_type VARCHAR(96) NOT NULL,
    schema_version INTEGER NOT NULL DEFAULT 1,
    durable BOOLEAN NOT NULL DEFAULT true,
    stream_seq BIGINT NULL,
    occurred_at DATETIME NOT NULL,
    actor_kind VARCHAR(16) NOT NULL,
    actor_id BIGINT NULL,
    authority_source VARCHAR(160) NOT NULL DEFAULT '',
    subject_kind VARCHAR(64) NOT NULL,
    subject_id BIGINT NOT NULL,
    subject_version BIGINT NOT NULL DEFAULT 0,
    topics TEXT NOT NULL,
    realtime_mode VARCHAR(8) NOT NULL DEFAULT 'SIGNAL',
    data TEXT NOT NULL,
    correlation_id VARCHAR(64) NOT NULL DEFAULT '',
    causation_id BIGINT NULL,
    sequenced_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_domain_event_actor CHECK (actor_kind IN ('HUMAN','AGENT','MACHINE','CLIENT','SYSTEM')),
    CONSTRAINT {{.Prefix}}chk_domain_event_realtime CHECK (realtime_mode IN ('NONE','SIGNAL','FULL'))
);
CREATE UNIQUE INDEX {{.Prefix}}idx_domain_events_sequence ON {{.Prefix}}domain_events (stream_seq);
CREATE INDEX {{.Prefix}}idx_domain_events_subject ON {{.Prefix}}domain_events (tenant_id, subject_kind, subject_id, stream_seq);
CREATE INDEX {{.Prefix}}idx_domain_events_type ON {{.Prefix}}domain_events (event_type, stream_seq);
CREATE INDEX {{.Prefix}}idx_domain_events_purge ON {{.Prefix}}domain_events (durable, occurred_at);
CREATE INDEX {{.Prefix}}idx_domain_events_pending ON {{.Prefix}}domain_events (id) WHERE stream_seq IS NULL;
-- +goose Down
DROP TABLE {{.Prefix}}domain_events;
