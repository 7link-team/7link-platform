-- +goose Up
-- Preserve existing assignments using the existing static-group representation.
-- IDs are globally allocated; a conflicting group ID fails the migration.
INSERT INTO {{.Prefix}}iam_groups
    (tenant_id, id, name, name_key, group_type, rule_json, description, status, sort_order, version, created_at, updated_at)
SELECT tenant_id, id, CONCAT('Group ', CAST(id AS TEXT), ': ', LEFT(name, 80)), LOWER(CONCAT('Group ', CAST(id AS TEXT), ': ', LEFT(name, 80))),
       'static', '', '', status, sort_order, version, created_at, updated_at
FROM {{.Prefix}}iam_positions;
INSERT INTO {{.Prefix}}iam_group_members
    (tenant_id, group_id, principal_id, starts_at, ends_at, created_at, updated_at)
SELECT tenant_id, position_id, principal_id, starts_at, ends_at, created_at, updated_at
FROM {{.Prefix}}iam_position_members;
INSERT INTO {{.Prefix}}iam_group_role_bindings (tenant_id, role_id, group_id, created_at)
SELECT tenant_id, role_id, position_id, created_at FROM {{.Prefix}}iam_position_role_bindings;
DELETE FROM {{.Prefix}}iam_position_role_bindings;
DELETE FROM {{.Prefix}}iam_position_members;
DELETE FROM {{.Prefix}}iam_positions;

-- +goose Down
-- Existing groups remain usable by the previous application version.
SELECT 1;
