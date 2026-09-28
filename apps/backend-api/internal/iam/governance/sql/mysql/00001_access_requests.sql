-- Source: internal/modules/governance/sql/mysql/00001_access_requests.sql:1-45.
-- Validation: internal/modules/governance/governance.go:142-152,228-311.
-- The tenant reference is added by 00003_constraints.sql.
-- MySQL DDL auto-commits; each base version is independently recoverable.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_access_requests (
    tenant_id          BIGINT       NOT NULL,
    id                 BIGINT       NOT NULL,
    requester_id       BIGINT       NOT NULL,
    role_id            BIGINT       NOT NULL,
    role_name          VARCHAR(128) NOT NULL,
    reason             TEXT         NOT NULL,
    snapshot_json      TEXT         NOT NULL,
    status             VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL,
    access_expires_at  DATETIME(3)  NOT NULL,
    request_expires_at DATETIME(3)  NOT NULL,
    decided_by         BIGINT       NULL,
    decision_note      TEXT         NOT NULL DEFAULT (''),
    decided_at         DATETIME(3)  NULL,
    revoked_by         BIGINT       NULL,
    revoke_reason      TEXT         NOT NULL DEFAULT (''),
    revoked_at         DATETIME(3)  NULL,
    created_at         DATETIME(3)  NOT NULL,
    updated_at         DATETIME(3)  NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_status
        CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled', 'revoked')),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_reason
        CHECK (OCTET_LENGTH(reason) >= 3 AND OCTET_LENGTH(reason) <= 500),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_decision_note
        CHECK (OCTET_LENGTH(decision_note) <= 500),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_revoke_reason
        CHECK (OCTET_LENGTH(revoke_reason) <= 500),
    INDEX {{.Prefix}}idx_iam_access_requests_queue (tenant_id, status, created_at),
    INDEX {{.Prefix}}idx_iam_access_requests_requester (tenant_id, requester_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_approval_steps (
    tenant_id BIGINT      NOT NULL,
    request_id BIGINT     NOT NULL,
    step       INT        NOT NULL,
    decision   VARCHAR(16) COLLATE utf8mb4_bin NOT NULL,
    decided_by BIGINT     NULL,
    decided_at DATETIME(3) NULL,
    note       TEXT       NOT NULL DEFAULT (''),
    PRIMARY KEY (tenant_id, request_id, step),
    CONSTRAINT {{.Prefix}}fk_iam_approval_steps_request
        FOREIGN KEY (tenant_id, request_id)
        REFERENCES {{.Prefix}}iam_access_requests (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_approval_steps_decision
        CHECK (decision IN ('pending', 'approved', 'rejected')),
    CONSTRAINT {{.Prefix}}chk_iam_approval_steps_note
        CHECK (OCTET_LENGTH(note) <= 500)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_approval_steps;
DROP TABLE {{.Prefix}}iam_access_requests;
