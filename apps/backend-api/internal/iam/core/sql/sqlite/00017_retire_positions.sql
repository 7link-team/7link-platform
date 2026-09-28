-- +goose Up
UPDATE {{.Prefix}}iam_resource_relationships SET subject_type = 'group' WHERE subject_type = 'position';
UPDATE {{.Prefix}}iam_relationships SET subject_type = 'group' WHERE subject_type = 'position';
UPDATE {{.Prefix}}iam_menus SET parent_id = NULL
WHERE (tenant_id, parent_id) IN (
    SELECT tenant_id, id FROM {{.Prefix}}iam_menus
    WHERE route IN ('/positions', '/iam/positions', '/job-positions') OR permission_code IN
        ('position_create', 'position_view', 'position_update', 'position_delete', 'position_manage_member')
);
DELETE FROM {{.Prefix}}iam_menus WHERE route IN ('/positions', '/iam/positions', '/job-positions') OR permission_code IN ('position_create', 'position_view', 'position_update', 'position_delete', 'position_manage_member');
DELETE FROM {{.Prefix}}iam_role_permissions WHERE permission_code IN
    ('position_create', 'position_view', 'position_update', 'position_delete', 'position_manage_member');

-- +goose Down
-- Data-only rollback retains the migrated group-based policy.
SELECT 1;
