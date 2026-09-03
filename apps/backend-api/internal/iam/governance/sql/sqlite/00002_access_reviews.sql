-- Sources: internal/modules/governance/sql/sqlite/00002_access_reviews.sql:1-42;
-- internal/modules/governance/sql/sqlite/00003_derived_review_grants.sql:1-5.
-- Validation: internal/modules/governance/review.go:142-145,242-245,363-396,420-630.
-- The tenant reference is added by 00003_constraints.sql.
-- +goose Up
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
CREATE INDEX {{.Prefix}}idx_iam_access_reviews_status
    ON {{.Prefix}}iam_access_reviews (tenant_id, status, due_at, created_at);

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
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_decision
    ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, decision, principal_id);
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_grant
    ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, grant_type, grant_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_access_review_items;
DROP TABLE {{.Prefix}}iam_access_reviews;
