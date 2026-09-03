-- Source: internal/modules/governance/sql/postgres/00001_access_requests.sql:1-43
-- +goose Up
CREATE TABLE {{.Prefix}}iam_access_requests (
    tenant_id BIGINT NOT NULL,
    id BIGINT  NOT NULL,
    requester_id BIGINT NOT NULL,
    role_id BIGINT  NOT NULL,
    role_name          VARCHAR(128) NOT NULL,
    reason             VARCHAR(500) NOT NULL,
    snapshot_json      TEXT         NOT NULL,
    status             VARCHAR(16)  NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled', 'revoked')),
    access_expires_at  BIGINT       NOT NULL,
    request_expires_at BIGINT       NOT NULL,
    decided_by BIGINT NOT NULL DEFAULT '',
    decision_note      VARCHAR(500) NOT NULL DEFAULT '',
    decided_at         BIGINT       NOT NULL DEFAULT 0,
    revoked_by BIGINT NOT NULL DEFAULT '',
    revoke_reason      VARCHAR(500) NOT NULL DEFAULT '',
    revoked_at         BIGINT       NOT NULL DEFAULT 0,
    created_at         BIGINT       NOT NULL,
    updated_at         BIGINT       NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_iam_access_requests_queue
    ON {{.Prefix}}iam_access_requests (tenant_id, status, created_at);
CREATE INDEX {{.Prefix}}idx_iam_access_requests_requester
    ON {{.Prefix}}iam_access_requests (tenant_id, requester_id, created_at);

CREATE TABLE {{.Prefix}}iam_approval_steps (
    tenant_id BIGINT NOT NULL,
    request_id BIGINT  NOT NULL,
    step       INTEGER      NOT NULL,
    decision   VARCHAR(16)  NOT NULL CHECK (decision IN ('pending', 'approved', 'rejected')),
    decided_by BIGINT NOT NULL DEFAULT '',
    decided_at BIGINT       NOT NULL DEFAULT 0,
    note       VARCHAR(500) NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, request_id, step),
    FOREIGN KEY (tenant_id, request_id) REFERENCES {{.Prefix}}iam_access_requests (tenant_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}iam_approval_steps;
DROP TABLE IF EXISTS {{.Prefix}}iam_access_requests;