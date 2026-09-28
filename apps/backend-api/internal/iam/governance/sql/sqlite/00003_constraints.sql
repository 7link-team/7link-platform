-- Cross-module tenant references preserve the source ON DELETE CASCADE behavior.
-- Sources: internal/modules/governance/sql/sqlite/00001_access_requests.sql:22;
-- internal/modules/governance/sql/sqlite/00002_access_reviews.sql:13.
-- SQLite cannot add or drop foreign keys in place, so each tenant-owned parent
-- is rebuilt together with its detail table.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_approval_steps
    RENAME TO {{.Prefix}}iam_approval_steps_old;
ALTER TABLE {{.Prefix}}iam_access_requests
    RENAME TO {{.Prefix}}iam_access_requests_old;
CREATE TABLE {{.Prefix}}iam_access_requests (
    tenant_id          BIGINT   NOT NULL,
    id                 BIGINT   NOT NULL,
    requester_id       BIGINT   NOT NULL,
    role_id            BIGINT   NOT NULL,
    role_name          TEXT     NOT NULL,
    reason             TEXT     NOT NULL,
    snapshot_json      TEXT     NOT NULL,
    status             TEXT     COLLATE BINARY NOT NULL,
    access_expires_at  DATETIME NOT NULL,
    request_expires_at DATETIME NOT NULL,
    decided_by         BIGINT   NULL,
    decision_note      TEXT     NOT NULL DEFAULT '',
    decided_at         DATETIME NULL,
    revoked_by         BIGINT   NULL,
    revoke_reason      TEXT     NOT NULL DEFAULT '',
    revoked_at         DATETIME NULL,
    created_at         DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}fk_iam_access_requests_tenant
        FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_status
        CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled', 'revoked')),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_reason
        CHECK (length(CAST(reason AS BLOB)) >= 3 AND length(CAST(reason AS BLOB)) <= 500),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_decision_note
        CHECK (length(CAST(decision_note AS BLOB)) <= 500),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_revoke_reason
        CHECK (length(CAST(revoke_reason AS BLOB)) <= 500)
);
INSERT INTO {{.Prefix}}iam_access_requests
SELECT * FROM {{.Prefix}}iam_access_requests_old;
CREATE TABLE {{.Prefix}}iam_approval_steps (
    tenant_id BIGINT   NOT NULL,
    request_id BIGINT  NOT NULL,
    step       INTEGER NOT NULL,
    decision   TEXT     COLLATE BINARY NOT NULL,
    decided_by BIGINT   NULL,
    decided_at DATETIME NULL,
    note       TEXT     NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, request_id, step),
    CONSTRAINT {{.Prefix}}fk_iam_approval_steps_request
        FOREIGN KEY (tenant_id, request_id)
        REFERENCES {{.Prefix}}iam_access_requests (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_approval_steps_decision
        CHECK (decision IN ('pending', 'approved', 'rejected')),
    CONSTRAINT {{.Prefix}}chk_iam_approval_steps_note
        CHECK (length(CAST(note AS BLOB)) <= 500)
);
INSERT INTO {{.Prefix}}iam_approval_steps
SELECT * FROM {{.Prefix}}iam_approval_steps_old;
DROP TABLE {{.Prefix}}iam_approval_steps_old;
DROP TABLE {{.Prefix}}iam_access_requests_old;
CREATE INDEX {{.Prefix}}idx_iam_access_requests_queue
    ON {{.Prefix}}iam_access_requests (tenant_id, status, created_at);
CREATE INDEX {{.Prefix}}idx_iam_access_requests_requester
    ON {{.Prefix}}iam_access_requests (tenant_id, requester_id, created_at);

ALTER TABLE {{.Prefix}}iam_access_review_items
    RENAME TO {{.Prefix}}iam_access_review_items_old;
ALTER TABLE {{.Prefix}}iam_access_reviews
    RENAME TO {{.Prefix}}iam_access_reviews_old;
CREATE TABLE {{.Prefix}}iam_access_reviews (
    tenant_id   BIGINT   NOT NULL,
    id          BIGINT   NOT NULL,
    name        TEXT     NOT NULL,
    owner_id    BIGINT   NOT NULL,
    status      TEXT     COLLATE BINARY NOT NULL,
    due_at      DATETIME NOT NULL,
    completed_at DATETIME NULL,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}fk_iam_access_reviews_tenant
        FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_access_reviews_status
        CHECK (status IN ('open', 'completed', 'cancelled'))
);
INSERT INTO {{.Prefix}}iam_access_reviews
SELECT * FROM {{.Prefix}}iam_access_reviews_old;
CREATE TABLE {{.Prefix}}iam_access_review_items (
    tenant_id        BIGINT   NOT NULL,
    review_id        BIGINT   NOT NULL,
    id               BIGINT   NOT NULL,
    principal_id     BIGINT   NOT NULL,
    principal_name   TEXT     NOT NULL,
    role_id          BIGINT   NOT NULL,
    role_name        TEXT     NOT NULL,
    grant_type       TEXT     COLLATE BINARY NOT NULL,
    grant_id         BIGINT   NULL,
    grant_created_at DATETIME NOT NULL,
    grant_expires_at DATETIME NULL,
    decision         TEXT     COLLATE BINARY NOT NULL,
    decided_by       BIGINT   NULL,
    decision_note    TEXT     NOT NULL DEFAULT '',
    decided_at       DATETIME NULL,
    PRIMARY KEY (tenant_id, review_id, id),
    CONSTRAINT {{.Prefix}}fk_iam_access_review_items_review
        FOREIGN KEY (tenant_id, review_id)
        REFERENCES {{.Prefix}}iam_access_reviews (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_access_review_items_grant_type
        CHECK (grant_type IN ('permanent', 'temporary', 'group', 'position',
            'entity', 'dynamic_group', 'relationship')),
    CONSTRAINT {{.Prefix}}chk_iam_access_review_items_decision
        CHECK (decision IN ('pending', 'keep', 'revoke')),
    CONSTRAINT {{.Prefix}}chk_iam_access_review_items_decision_note
        CHECK (length(CAST(decision_note AS BLOB)) <= 500)
);
INSERT INTO {{.Prefix}}iam_access_review_items
SELECT * FROM {{.Prefix}}iam_access_review_items_old;
DROP TABLE {{.Prefix}}iam_access_review_items_old;
DROP TABLE {{.Prefix}}iam_access_reviews_old;
CREATE INDEX {{.Prefix}}idx_iam_access_reviews_status
    ON {{.Prefix}}iam_access_reviews (tenant_id, status, due_at, created_at);
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_decision
    ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, decision, principal_id);
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_grant
    ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, grant_type, grant_id);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_access_review_items
    RENAME TO {{.Prefix}}iam_access_review_items_old;
ALTER TABLE {{.Prefix}}iam_access_reviews
    RENAME TO {{.Prefix}}iam_access_reviews_old;
CREATE TABLE {{.Prefix}}iam_access_reviews (
    tenant_id   BIGINT   NOT NULL,
    id          BIGINT   NOT NULL,
    name        TEXT     NOT NULL,
    owner_id    BIGINT   NOT NULL,
    status      TEXT     COLLATE BINARY NOT NULL,
    due_at      DATETIME NOT NULL,
    completed_at DATETIME NULL,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}chk_iam_access_reviews_status
        CHECK (status IN ('open', 'completed', 'cancelled'))
);
INSERT INTO {{.Prefix}}iam_access_reviews
SELECT * FROM {{.Prefix}}iam_access_reviews_old;
CREATE TABLE {{.Prefix}}iam_access_review_items (
    tenant_id        BIGINT   NOT NULL,
    review_id        BIGINT   NOT NULL,
    id               BIGINT   NOT NULL,
    principal_id     BIGINT   NOT NULL,
    principal_name   TEXT     NOT NULL,
    role_id          BIGINT   NOT NULL,
    role_name        TEXT     NOT NULL,
    grant_type       TEXT     COLLATE BINARY NOT NULL,
    grant_id         BIGINT   NULL,
    grant_created_at DATETIME NOT NULL,
    grant_expires_at DATETIME NULL,
    decision         TEXT     COLLATE BINARY NOT NULL,
    decided_by       BIGINT   NULL,
    decision_note    TEXT     NOT NULL DEFAULT '',
    decided_at       DATETIME NULL,
    PRIMARY KEY (tenant_id, review_id, id),
    CONSTRAINT {{.Prefix}}fk_iam_access_review_items_review
        FOREIGN KEY (tenant_id, review_id)
        REFERENCES {{.Prefix}}iam_access_reviews (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_access_review_items_grant_type
        CHECK (grant_type IN ('permanent', 'temporary', 'group', 'position',
            'entity', 'dynamic_group', 'relationship')),
    CONSTRAINT {{.Prefix}}chk_iam_access_review_items_decision
        CHECK (decision IN ('pending', 'keep', 'revoke')),
    CONSTRAINT {{.Prefix}}chk_iam_access_review_items_decision_note
        CHECK (length(CAST(decision_note AS BLOB)) <= 500)
);
INSERT INTO {{.Prefix}}iam_access_review_items
SELECT * FROM {{.Prefix}}iam_access_review_items_old;
DROP TABLE {{.Prefix}}iam_access_review_items_old;
DROP TABLE {{.Prefix}}iam_access_reviews_old;
CREATE INDEX {{.Prefix}}idx_iam_access_reviews_status
    ON {{.Prefix}}iam_access_reviews (tenant_id, status, due_at, created_at);
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_decision
    ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, decision, principal_id);
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_grant
    ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, grant_type, grant_id);

ALTER TABLE {{.Prefix}}iam_approval_steps
    RENAME TO {{.Prefix}}iam_approval_steps_old;
ALTER TABLE {{.Prefix}}iam_access_requests
    RENAME TO {{.Prefix}}iam_access_requests_old;
CREATE TABLE {{.Prefix}}iam_access_requests (
    tenant_id          BIGINT   NOT NULL,
    id                 BIGINT   NOT NULL,
    requester_id       BIGINT   NOT NULL,
    role_id            BIGINT   NOT NULL,
    role_name          TEXT     NOT NULL,
    reason             TEXT     NOT NULL,
    snapshot_json      TEXT     NOT NULL,
    status             TEXT     COLLATE BINARY NOT NULL,
    access_expires_at  DATETIME NOT NULL,
    request_expires_at DATETIME NOT NULL,
    decided_by         BIGINT   NULL,
    decision_note      TEXT     NOT NULL DEFAULT '',
    decided_at         DATETIME NULL,
    revoked_by         BIGINT   NULL,
    revoke_reason      TEXT     NOT NULL DEFAULT '',
    revoked_at         DATETIME NULL,
    created_at         DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_status
        CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled', 'revoked')),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_reason
        CHECK (length(CAST(reason AS BLOB)) >= 3 AND length(CAST(reason AS BLOB)) <= 500),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_decision_note
        CHECK (length(CAST(decision_note AS BLOB)) <= 500),
    CONSTRAINT {{.Prefix}}chk_iam_access_requests_revoke_reason
        CHECK (length(CAST(revoke_reason AS BLOB)) <= 500)
);
INSERT INTO {{.Prefix}}iam_access_requests
SELECT * FROM {{.Prefix}}iam_access_requests_old;
CREATE TABLE {{.Prefix}}iam_approval_steps (
    tenant_id BIGINT   NOT NULL,
    request_id BIGINT  NOT NULL,
    step       INTEGER NOT NULL,
    decision   TEXT     COLLATE BINARY NOT NULL,
    decided_by BIGINT   NULL,
    decided_at DATETIME NULL,
    note       TEXT     NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, request_id, step),
    CONSTRAINT {{.Prefix}}fk_iam_approval_steps_request
        FOREIGN KEY (tenant_id, request_id)
        REFERENCES {{.Prefix}}iam_access_requests (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_approval_steps_decision
        CHECK (decision IN ('pending', 'approved', 'rejected')),
    CONSTRAINT {{.Prefix}}chk_iam_approval_steps_note
        CHECK (length(CAST(note AS BLOB)) <= 500)
);
INSERT INTO {{.Prefix}}iam_approval_steps
SELECT * FROM {{.Prefix}}iam_approval_steps_old;
DROP TABLE {{.Prefix}}iam_approval_steps_old;
DROP TABLE {{.Prefix}}iam_access_requests_old;
CREATE INDEX {{.Prefix}}idx_iam_access_requests_queue
    ON {{.Prefix}}iam_access_requests (tenant_id, status, created_at);
CREATE INDEX {{.Prefix}}idx_iam_access_requests_requester
    ON {{.Prefix}}iam_access_requests (tenant_id, requester_id, created_at);
