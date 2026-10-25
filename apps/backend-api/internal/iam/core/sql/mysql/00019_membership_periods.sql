-- +goose Up
CREATE TABLE {{.Prefix}}iam_membership_periods (
    tenant_id BIGINT NOT NULL,
    id BIGINT NOT NULL,
    principal_id BIGINT NOT NULL,
    started_at DATETIME(3) NOT NULL,
    ended_at DATETIME(3) NULL,
    CONSTRAINT {{.Prefix}}pk_membership_periods PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}ck_membership_period_order CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE INDEX {{.Prefix}}idx_membership_period_subject ON {{.Prefix}}iam_membership_periods (tenant_id, principal_id, started_at);
-- Existing CreatedAt/DisabledAt values cannot prove earlier reactivation
-- history. Begin observation now rather than inventing previous intervals.
INSERT INTO {{.Prefix}}iam_membership_periods (tenant_id, id, principal_id, started_at)
SELECT tenant_id, principal_id, principal_id, UTC_TIMESTAMP(3)
FROM {{.Prefix}}iam_tenant_members WHERE status = 'active';

-- +goose Down
DROP TABLE {{.Prefix}}iam_membership_periods;
