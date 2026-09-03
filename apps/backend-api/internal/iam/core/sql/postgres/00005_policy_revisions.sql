-- Source: internal/modules/iam/sql/postgres/00010_policy_revision.sql:2-6.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_policy_revisions (
    tenant_id  BIGINT         NOT NULL PRIMARY KEY,
    revision   BIGINT         NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_policy_revisions_revision CHECK (revision >= 0)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_policy_revisions;
