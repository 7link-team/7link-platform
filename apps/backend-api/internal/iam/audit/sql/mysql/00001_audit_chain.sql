-- Sources: internal/modules/iam/sql/mysql/00003_local_identity.sql:57-62;
-- internal/modules/iam/sql/mysql/00006_audit_chain.sql:2-27.
-- Validation widths: internal/modules/audit/audit.go:607-612.
-- Fresh-database baseline: every event has a sequence and event hash; an empty
-- previous hash identifies the first event in a tenant's chain.
-- MySQL DDL auto-commits; recover a partial application using the migration runbook.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_audit_events (
    id            BIGINT       NOT NULL PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL,
    principal_id  BIGINT       NULL,
    event_type    VARCHAR(64)  COLLATE utf8mb4_bin NOT NULL,
    target_type   VARCHAR(64)  COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    target_id     BIGINT       NULL,
    outcome       VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL,
    ip_address    VARCHAR(64)  COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    user_agent    VARCHAR(512) NOT NULL DEFAULT '',
    detail        TEXT         NOT NULL DEFAULT ('{}'),
    created_at    DATETIME(3)  NOT NULL,
    sequence      BIGINT       NOT NULL,
    previous_hash CHAR(64)     COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    event_hash    CHAR(64)     COLLATE utf8mb4_bin NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_audit_events_outcome
        CHECK (outcome IN ('success', 'denied', 'failure')),
    INDEX {{.Prefix}}idx_iam_audit_tenant_time (tenant_id, created_at),
    INDEX {{.Prefix}}idx_iam_audit_principal_time (principal_id, created_at),
    UNIQUE INDEX {{.Prefix}}uq_iam_audit_chain_sequence (tenant_id, sequence),
    UNIQUE INDEX {{.Prefix}}uq_iam_audit_chain_hash (tenant_id, event_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_audit_heads (
    tenant_id  BIGINT      NOT NULL PRIMARY KEY,
    sequence   BIGINT      NOT NULL DEFAULT 0,
    event_hash CHAR(64)    COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    updated_at DATETIME(3) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- UPDATE/DELETE triggers do not cover privileged maintenance paths. Production
-- credentials must not have privileges that can disable or drop these triggers.
DROP TRIGGER IF EXISTS {{.Prefix}}trg_iam_audit_events_no_delete;
DROP TRIGGER IF EXISTS {{.Prefix}}trg_iam_audit_events_no_update;
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_audit_events_no_update
BEFORE UPDATE ON {{.Prefix}}iam_audit_events FOR EACH ROW
BEGIN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'iam_audit_events is append-only';
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_audit_events_no_delete
BEFORE DELETE ON {{.Prefix}}iam_audit_events FOR EACH ROW
BEGIN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'iam_audit_events is append-only';
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS {{.Prefix}}trg_iam_audit_events_no_delete;
DROP TRIGGER IF EXISTS {{.Prefix}}trg_iam_audit_events_no_update;
DROP TABLE {{.Prefix}}iam_audit_heads;
DROP TABLE {{.Prefix}}iam_audit_events;
