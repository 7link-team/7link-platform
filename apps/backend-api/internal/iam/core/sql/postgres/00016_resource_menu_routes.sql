-- +goose Up
UPDATE {{.Prefix}}iam_menus
SET route = CASE route
    WHEN '/iam/users' THEN '/users'
    WHEN '/iam/invitations' THEN '/invitations'
    WHEN '/iam/service-accounts' THEN '/service-accounts'
    WHEN '/iam/entities' THEN '/entities'
    WHEN '/iam/departments' THEN '/departments'
    WHEN '/iam/positions' THEN '/positions'
    WHEN '/iam/groups' THEN '/groups'
    WHEN '/iam/roles' THEN '/roles'
    WHEN '/iam/access-requests' THEN '/access-requests'
    WHEN '/iam/access-reviews' THEN '/access-reviews'
    WHEN '/iam/menus' THEN '/menus'
    WHEN '/iam/oauth-clients' THEN '/oauth-clients'
    WHEN '/iam/scim-directories' THEN '/scim-directories'
    WHEN '/iam/audit-events' THEN '/audit/events'
    WHEN '/iam/audit-governance' THEN '/audit-governance'
END
WHERE route IN (
    '/iam/users', '/iam/invitations', '/iam/service-accounts', '/iam/entities',
    '/iam/departments', '/iam/positions', '/iam/groups', '/iam/roles',
    '/iam/access-requests', '/iam/access-reviews', '/iam/menus', '/iam/oauth-clients',
    '/iam/scim-directories', '/iam/audit-events', '/iam/audit-governance'
);

-- +goose Down
-- Data-only rollback preserves current routes and any subsequent administrator edits.
SELECT 1;
