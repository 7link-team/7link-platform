-- Sources: internal/modules/iam/sql/sqlite/00003_local_identity.sql:128-142;
-- internal/modules/iam/sql/sqlite/00006_audit_chain.sql:2-26.
-- Validation widths: internal/modules/audit/audit.go:607-612.
-- Fresh-database baseline: every event has a sequence and event hash; an empty
-- previous hash identifies the first event in a tenant's chain.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_audit_events (
    id            BIGINT   NOT NULL PRIMARY KEY,
    tenant_id     BIGINT   NOT NULL,
    principal_id  BIGINT   NULL,
    event_type    TEXT     COLLATE BINARY NOT NULL,
    target_type   TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    target_id     BIGINT   NULL,
    outcome       TEXT     COLLATE BINARY NOT NULL,
    ip_address    TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    user_agent    TEXT     NOT NULL DEFAULT '',
    detail        TEXT     NOT NULL DEFAULT '{}',
    created_at    DATETIME NOT NULL,
    sequence      BIGINT   NOT NULL,
    previous_hash TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    event_hash    TEXT     COLLATE BINARY NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_audit_events_outcome
        CHECK (outcome IN ('success', 'denied', 'failure'))
);

CREATE INDEX {{.Prefix}}idx_iam_audit_tenant_time
    ON {{.Prefix}}iam_audit_events (tenant_id, created_at);
CREATE INDEX {{.Prefix}}idx_iam_audit_principal_time
    ON {{.Prefix}}iam_audit_events (principal_id, created_at);
CREATE UNIQUE INDEX {{.Prefix}}uq_iam_audit_chain_sequence
    ON {{.Prefix}}iam_audit_events (tenant_id, sequence);
CREATE UNIQUE INDEX {{.Prefix}}uq_iam_audit_chain_hash
    ON {{.Prefix}}iam_audit_events (tenant_id, event_hash);

CREATE TABLE {{.Prefix}}iam_audit_heads (
    tenant_id  BIGINT   NOT NULL PRIMARY KEY,
    sequence   BIGINT   NOT NULL DEFAULT 0,
    event_hash TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL
);

-- UPDATE/DELETE triggers cannot protect privileged maintenance paths. Production
-- credentials must not have privileges that can disable or drop these triggers.
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_audit_events_no_update
BEFORE UPDATE ON {{.Prefix}}iam_audit_events
BEGIN
    SELECT RAISE(ABORT, 'iam_audit_events is append-only');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_audit_events_no_delete
BEFORE DELETE ON {{.Prefix}}iam_audit_events
BEGIN
    SELECT RAISE(ABORT, 'iam_audit_events is append-only');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER {{.Prefix}}trg_iam_audit_events_no_delete;
DROP TRIGGER {{.Prefix}}trg_iam_audit_events_no_update;
DROP TABLE {{.Prefix}}iam_audit_heads;
DROP TABLE {{.Prefix}}iam_audit_events;
