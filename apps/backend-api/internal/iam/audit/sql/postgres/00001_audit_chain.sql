-- Sources: internal/modules/iam/sql/postgres/00003_local_identity.sql:62-69;
-- internal/modules/iam/sql/postgres/00006_audit_chain.sql:2-21.
-- Validation widths: internal/modules/audit/audit.go:607-612.
-- Fresh-database baseline: every event has a sequence and event hash; an empty
-- previous hash identifies the first event in a tenant's chain.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_audit_events (
    id            BIGINT         NOT NULL PRIMARY KEY,
    tenant_id     BIGINT         NOT NULL,
    principal_id  BIGINT         NULL,
    event_type    VARCHAR(64)    COLLATE "C" NOT NULL,
    target_type   VARCHAR(64)    COLLATE "C" NOT NULL DEFAULT '',
    target_id     BIGINT         NULL,
    outcome       VARCHAR(16)    COLLATE "C" NOT NULL,
    ip_address    VARCHAR(64)    COLLATE "C" NOT NULL DEFAULT '',
    user_agent    VARCHAR(512)   NOT NULL DEFAULT '',
    detail        TEXT           NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ(3) NOT NULL,
    sequence      BIGINT         NOT NULL,
    previous_hash CHAR(64)       COLLATE "C" NOT NULL DEFAULT '',
    event_hash    CHAR(64)       COLLATE "C" NOT NULL,
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
    tenant_id  BIGINT         NOT NULL PRIMARY KEY,
    sequence   BIGINT         NOT NULL DEFAULT 0,
    event_hash CHAR(64)       COLLATE "C" NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ(3) NOT NULL
);

-- UPDATE/DELETE triggers do not stop TRUNCATE, and privileged accounts can
-- disable or drop them. Production credentials must not have those privileges.
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}reject_iam_audit_event_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'iam_audit_events is append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}trg_iam_audit_events_no_update
BEFORE UPDATE ON {{.Prefix}}iam_audit_events
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}reject_iam_audit_event_change();
CREATE TRIGGER {{.Prefix}}trg_iam_audit_events_no_delete
BEFORE DELETE ON {{.Prefix}}iam_audit_events
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}reject_iam_audit_event_change();

-- +goose Down
DROP TRIGGER {{.Prefix}}trg_iam_audit_events_no_delete ON {{.Prefix}}iam_audit_events;
DROP TRIGGER {{.Prefix}}trg_iam_audit_events_no_update ON {{.Prefix}}iam_audit_events;
DROP FUNCTION {{.Prefix}}reject_iam_audit_event_change();
DROP TABLE {{.Prefix}}iam_audit_heads;
DROP TABLE {{.Prefix}}iam_audit_events;
