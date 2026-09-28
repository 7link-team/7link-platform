-- Sources: internal/modules/governance/sql/postgres/00002_access_reviews.sql:1-42;
-- internal/modules/governance/sql/postgres/00003_derived_review_grants.sql:1-5.
-- Validation: internal/modules/governance/review.go:142-145,242-245,363-396,420-630.
-- The tenant reference is added by 00003_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_access_reviews (
    tenant_id   BIGINT         NOT NULL,
    id          BIGINT         NOT NULL,
    name        VARCHAR(128)   NOT NULL,
    owner_id    BIGINT         NOT NULL,
    status      VARCHAR(16)    COLLATE "C" NOT NULL,
    due_at      TIMESTAMPTZ(3) NOT NULL,
    completed_at TIMESTAMPTZ(3) NULL,
    created_at  TIMESTAMPTZ(3) NOT NULL,
    updated_at  TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}chk_iam_access_reviews_status
        CHECK (status IN ('open', 'completed', 'cancelled'))
);
CREATE INDEX {{.Prefix}}idx_iam_access_reviews_status
    ON {{.Prefix}}iam_access_reviews (tenant_id, status, due_at, created_at);

CREATE TABLE {{.Prefix}}iam_access_review_items (
    tenant_id       BIGINT         NOT NULL,
    review_id       BIGINT         NOT NULL,
    id              BIGINT         NOT NULL,
    principal_id    BIGINT         NOT NULL,
    principal_name  VARCHAR(255)   NOT NULL,
    role_id         BIGINT         NOT NULL,
    role_name       VARCHAR(128)   NOT NULL,
    grant_type      VARCHAR(16)    COLLATE "C" NOT NULL,
    grant_id        BIGINT         NULL,
    grant_created_at TIMESTAMPTZ(3) NOT NULL,
    grant_expires_at TIMESTAMPTZ(3) NULL,
    decision        VARCHAR(16)    COLLATE "C" NOT NULL,
    decided_by      BIGINT         NULL,
    decision_note   TEXT           NOT NULL DEFAULT '',
    decided_at      TIMESTAMPTZ(3) NULL,
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
        CHECK (octet_length(decision_note) <= 500)
);
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_decision
    ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, decision, principal_id);
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_grant
    ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, grant_type, grant_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_access_review_items;
DROP TABLE {{.Prefix}}iam_access_reviews;
