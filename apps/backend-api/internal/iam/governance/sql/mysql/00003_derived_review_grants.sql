-- Source: internal/modules/governance/sql/mysql/00003_derived_review_grants.sql:1-5
-- +goose Up
CREATE INDEX {{.Prefix}}idx_iam_access_review_items_grant ON {{.Prefix}}iam_access_review_items (tenant_id, review_id, grant_type, grant_id);

-- +goose Down
DROP INDEX {{.Prefix}}idx_iam_access_review_items_grant;