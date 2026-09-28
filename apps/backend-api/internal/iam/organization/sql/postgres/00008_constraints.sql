-- Cross-module references preserve the source ON DELETE behavior.
-- Sources: internal/modules/organization/sql/postgres/00002_positions.sql:27;
-- 00003_groups.sql:30; 00004_directory_role_bindings.sql:8,19;
-- 00006_iam_data_scopes.sql:8,19; 00007_invitations.sql:30.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_position_members
    ADD CONSTRAINT {{.Prefix}}fk_iam_position_members_principal
    FOREIGN KEY (tenant_id, principal_id)
    REFERENCES {{.Prefix}}iam_tenant_members (tenant_id, principal_id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_group_members
    ADD CONSTRAINT {{.Prefix}}fk_iam_group_members_principal
    FOREIGN KEY (tenant_id, principal_id)
    REFERENCES {{.Prefix}}iam_tenant_members (tenant_id, principal_id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_group_role_bindings
    ADD CONSTRAINT {{.Prefix}}fk_iam_group_role_bindings_role
    FOREIGN KEY (tenant_id, role_id)
    REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_position_role_bindings
    ADD CONSTRAINT {{.Prefix}}fk_iam_position_role_bindings_role
    FOREIGN KEY (tenant_id, role_id)
    REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_member_departments
    ADD CONSTRAINT {{.Prefix}}fk_iam_member_departments_member
    FOREIGN KEY (tenant_id, principal_id)
    REFERENCES {{.Prefix}}iam_tenant_members (tenant_id, principal_id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_role_data_scopes
    ADD CONSTRAINT {{.Prefix}}fk_iam_role_data_scopes_role
    FOREIGN KEY (tenant_id, role_id)
    REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_invitation_roles
    ADD CONSTRAINT {{.Prefix}}fk_iam_invitation_roles_role
    FOREIGN KEY (tenant_id, role_id)
    REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE RESTRICT;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_invitation_roles
    DROP CONSTRAINT {{.Prefix}}fk_iam_invitation_roles_role;
ALTER TABLE {{.Prefix}}iam_role_data_scopes
    DROP CONSTRAINT {{.Prefix}}fk_iam_role_data_scopes_role;
ALTER TABLE {{.Prefix}}iam_member_departments
    DROP CONSTRAINT {{.Prefix}}fk_iam_member_departments_member;
ALTER TABLE {{.Prefix}}iam_position_role_bindings
    DROP CONSTRAINT {{.Prefix}}fk_iam_position_role_bindings_role;
ALTER TABLE {{.Prefix}}iam_group_role_bindings
    DROP CONSTRAINT {{.Prefix}}fk_iam_group_role_bindings_role;
ALTER TABLE {{.Prefix}}iam_group_members
    DROP CONSTRAINT {{.Prefix}}fk_iam_group_members_principal;
ALTER TABLE {{.Prefix}}iam_position_members
    DROP CONSTRAINT {{.Prefix}}fk_iam_position_members_principal;
