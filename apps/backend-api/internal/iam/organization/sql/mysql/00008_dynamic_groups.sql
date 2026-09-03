-- Source: internal/modules/organization/sql/mysql/00008_dynamic_groups.sql:1-5
-- +goose Up
CREATE INDEX {{.Prefix}}idx_iam_groups_dynamic ON {{.Prefix}}iam_groups (tenant_id, group_type, status);

-- +goose Down
DROP INDEX {{.Prefix}}idx_iam_groups_dynamic;