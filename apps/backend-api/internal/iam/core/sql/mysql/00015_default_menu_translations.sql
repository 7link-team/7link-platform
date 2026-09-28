-- +goose Up
-- Backfill only untouched built-in menus. Custom names and existing translations are preserved.
UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Accounts and members","zh-CN":"账号与成员","ms-MY":"Akaun dan ahli"}'
WHERE route = '/iam/users' AND BINARY label IN ('Accounts and members', 'Principals and members') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Invitations","zh-CN":"邀请管理","ms-MY":"Jemputan"}'
WHERE route = '/iam/invitations' AND BINARY label IN ('Invitations') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Service accounts","zh-CN":"服务账号","ms-MY":"Akaun perkhidmatan"}'
WHERE route = '/iam/service-accounts' AND BINARY label IN ('Service accounts') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Entities","zh-CN":"业务主体","ms-MY":"Entiti perniagaan"}'
WHERE route = '/iam/entities' AND BINARY label IN ('Entities') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Departments","zh-CN":"组织部门","ms-MY":"Jabatan"}'
WHERE route = '/iam/departments' AND BINARY label IN ('Departments') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Positions","zh-CN":"岗位","ms-MY":"Jawatan"}'
WHERE route = '/iam/positions' AND BINARY label IN ('Positions') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Groups","zh-CN":"用户组","ms-MY":"Kumpulan pengguna"}'
WHERE route = '/iam/groups' AND BINARY label IN ('Groups') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Roles and permissions","zh-CN":"角色与权限","ms-MY":"Peranan dan kebenaran"}'
WHERE route = '/iam/roles' AND BINARY label IN ('Roles and permissions') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Access requests","zh-CN":"访问申请","ms-MY":"Permintaan akses"}'
WHERE route = '/iam/access-requests' AND BINARY label IN ('Access requests') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Access reviews","zh-CN":"访问评审","ms-MY":"Semakan akses"}'
WHERE route = '/iam/access-reviews' AND BINARY label IN ('Access reviews') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Menu metadata","zh-CN":"菜单管理","ms-MY":"Pengurusan menu"}'
WHERE route = '/iam/menus' AND BINARY label IN ('Menu metadata') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"OAuth clients","zh-CN":"OAuth 客户端","ms-MY":"Klien OAuth"}'
WHERE route = '/iam/oauth-clients' AND BINARY label IN ('OAuth clients') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"SCIM directories","zh-CN":"SCIM 目录","ms-MY":"Direktori SCIM"}'
WHERE route = '/iam/scim-directories' AND BINARY label IN ('SCIM directories') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Audit events","zh-CN":"审计事件","ms-MY":"Peristiwa audit"}'
WHERE route = '/iam/audit-events' AND BINARY label IN ('Audit events') AND translations = '{}';

UPDATE {{.Prefix}}iam_menus
SET translations = '{"en-US":"Audit governance","zh-CN":"审计治理","ms-MY":"Tadbir urus audit"}'
WHERE route = '/iam/audit-governance' AND BINARY label IN ('Audit governance') AND translations = '{}';

-- +goose Down
-- Data-only rollback deliberately preserves translations, including subsequent user edits.
SELECT 1;
