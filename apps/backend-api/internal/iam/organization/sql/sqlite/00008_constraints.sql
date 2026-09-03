-- Cross-module references preserve the source ON DELETE behavior.
-- Sources: internal/modules/organization/sql/sqlite/00002_positions.sql:27;
-- 00003_groups.sql:30; 00004_directory_role_bindings.sql:8,19;
-- 00006_iam_data_scopes.sql:8,19; 00007_invitations.sql:30.
-- SQLite cannot add or drop foreign keys in place, so this milestone rebuilds
-- the seven cross-module child tables. The role-scope child is rebuilt with
-- its parent so SQLite never retains a reference to the renamed old table.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_position_members
    RENAME TO {{.Prefix}}iam_position_members_old;
CREATE TABLE {{.Prefix}}iam_position_members (
    tenant_id    BIGINT   NOT NULL,
    position_id  BIGINT   NOT NULL,
    principal_id BIGINT   NOT NULL,
    starts_at    DATETIME NULL,
    ends_at      DATETIME NULL,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, position_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_position_members_position
        FOREIGN KEY (tenant_id, position_id)
        REFERENCES {{.Prefix}}iam_positions (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_position_members_principal
        FOREIGN KEY (tenant_id, principal_id)
        REFERENCES {{.Prefix}}iam_tenant_members (tenant_id, principal_id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_position_members_window
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
INSERT INTO {{.Prefix}}iam_position_members
SELECT * FROM {{.Prefix}}iam_position_members_old;
DROP TABLE {{.Prefix}}iam_position_members_old;
CREATE INDEX {{.Prefix}}idx_iam_position_members_principal
    ON {{.Prefix}}iam_position_members
        (tenant_id, principal_id, starts_at, ends_at, position_id);

ALTER TABLE {{.Prefix}}iam_group_members
    RENAME TO {{.Prefix}}iam_group_members_old;
CREATE TABLE {{.Prefix}}iam_group_members (
    tenant_id    BIGINT   NOT NULL,
    group_id     BIGINT   NOT NULL,
    principal_id BIGINT   NOT NULL,
    starts_at    DATETIME NULL,
    ends_at      DATETIME NULL,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, group_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_group_members_group
        FOREIGN KEY (tenant_id, group_id)
        REFERENCES {{.Prefix}}iam_groups (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_group_members_principal
        FOREIGN KEY (tenant_id, principal_id)
        REFERENCES {{.Prefix}}iam_tenant_members (tenant_id, principal_id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_group_members_window
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
INSERT INTO {{.Prefix}}iam_group_members
SELECT * FROM {{.Prefix}}iam_group_members_old;
DROP TABLE {{.Prefix}}iam_group_members_old;
CREATE INDEX {{.Prefix}}idx_iam_group_members_principal
    ON {{.Prefix}}iam_group_members
        (tenant_id, principal_id, starts_at, ends_at, group_id);

ALTER TABLE {{.Prefix}}iam_group_role_bindings
    RENAME TO {{.Prefix}}iam_group_role_bindings_old;
CREATE TABLE {{.Prefix}}iam_group_role_bindings (
    tenant_id  BIGINT   NOT NULL,
    role_id    BIGINT   NOT NULL,
    group_id   BIGINT   NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id, group_id),
    CONSTRAINT {{.Prefix}}fk_iam_group_role_bindings_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_group_role_bindings_group
        FOREIGN KEY (tenant_id, group_id)
        REFERENCES {{.Prefix}}iam_groups (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_group_role_bindings
SELECT * FROM {{.Prefix}}iam_group_role_bindings_old;
DROP TABLE {{.Prefix}}iam_group_role_bindings_old;
CREATE INDEX {{.Prefix}}idx_iam_group_role_bindings_group
    ON {{.Prefix}}iam_group_role_bindings (tenant_id, group_id, role_id);

ALTER TABLE {{.Prefix}}iam_position_role_bindings
    RENAME TO {{.Prefix}}iam_position_role_bindings_old;
CREATE TABLE {{.Prefix}}iam_position_role_bindings (
    tenant_id  BIGINT   NOT NULL,
    role_id    BIGINT   NOT NULL,
    position_id BIGINT  NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id, position_id),
    CONSTRAINT {{.Prefix}}fk_iam_position_role_bindings_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_position_role_bindings_position
        FOREIGN KEY (tenant_id, position_id)
        REFERENCES {{.Prefix}}iam_positions (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_position_role_bindings
SELECT * FROM {{.Prefix}}iam_position_role_bindings_old;
DROP TABLE {{.Prefix}}iam_position_role_bindings_old;
CREATE INDEX {{.Prefix}}idx_iam_position_role_bindings_position
    ON {{.Prefix}}iam_position_role_bindings (tenant_id, position_id, role_id);

ALTER TABLE {{.Prefix}}iam_member_departments
    RENAME TO {{.Prefix}}iam_member_departments_old;
CREATE TABLE {{.Prefix}}iam_member_departments (
    tenant_id     BIGINT   NOT NULL,
    principal_id  BIGINT   NOT NULL,
    department_id BIGINT   NOT NULL,
    updated_at    DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_member_departments_member
        FOREIGN KEY (tenant_id, principal_id)
        REFERENCES {{.Prefix}}iam_tenant_members (tenant_id, principal_id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_member_departments_department
        FOREIGN KEY (tenant_id, department_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_member_departments
SELECT * FROM {{.Prefix}}iam_member_departments_old;
DROP TABLE {{.Prefix}}iam_member_departments_old;
CREATE INDEX {{.Prefix}}idx_iam_member_departments_department
    ON {{.Prefix}}iam_member_departments (tenant_id, department_id, principal_id);

ALTER TABLE {{.Prefix}}iam_role_scope_departments
    RENAME TO {{.Prefix}}iam_role_scope_departments_old;
ALTER TABLE {{.Prefix}}iam_role_data_scopes
    RENAME TO {{.Prefix}}iam_role_data_scopes_old;
CREATE TABLE {{.Prefix}}iam_role_data_scopes (
    tenant_id  BIGINT   NOT NULL,
    role_id    BIGINT   NOT NULL,
    scope_type TEXT     COLLATE BINARY NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id),
    CONSTRAINT {{.Prefix}}fk_iam_role_data_scopes_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_role_data_scopes_type
        CHECK (scope_type IN ('all', 'self', 'department',
            'department_and_descendants', 'selected_departments'))
);
INSERT INTO {{.Prefix}}iam_role_data_scopes
SELECT * FROM {{.Prefix}}iam_role_data_scopes_old;
CREATE TABLE {{.Prefix}}iam_role_scope_departments (
    tenant_id     BIGINT   NOT NULL,
    role_id       BIGINT   NOT NULL,
    department_id BIGINT   NOT NULL,
    created_at    DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id, department_id),
    CONSTRAINT {{.Prefix}}fk_iam_role_scope_departments_scope
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_role_data_scopes (tenant_id, role_id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_role_scope_departments_department
        FOREIGN KEY (tenant_id, department_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_role_scope_departments
SELECT * FROM {{.Prefix}}iam_role_scope_departments_old;
DROP TABLE {{.Prefix}}iam_role_scope_departments_old;
DROP TABLE {{.Prefix}}iam_role_data_scopes_old;
CREATE INDEX {{.Prefix}}idx_iam_role_scope_departments_department
    ON {{.Prefix}}iam_role_scope_departments (tenant_id, department_id, role_id);

ALTER TABLE {{.Prefix}}iam_invitation_roles
    RENAME TO {{.Prefix}}iam_invitation_roles_old;
CREATE TABLE {{.Prefix}}iam_invitation_roles (
    tenant_id     BIGINT NOT NULL,
    invitation_id BIGINT NOT NULL,
    role_id       BIGINT NOT NULL,
    PRIMARY KEY (tenant_id, invitation_id, role_id),
    CONSTRAINT {{.Prefix}}fk_iam_invitation_roles_invitation
        FOREIGN KEY (tenant_id, invitation_id)
        REFERENCES {{.Prefix}}iam_invitations (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_invitation_roles_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_invitation_roles
SELECT * FROM {{.Prefix}}iam_invitation_roles_old;
DROP TABLE {{.Prefix}}iam_invitation_roles_old;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_invitation_roles
    RENAME TO {{.Prefix}}iam_invitation_roles_old;
CREATE TABLE {{.Prefix}}iam_invitation_roles (
    tenant_id     BIGINT NOT NULL,
    invitation_id BIGINT NOT NULL,
    role_id       BIGINT NOT NULL,
    PRIMARY KEY (tenant_id, invitation_id, role_id),
    CONSTRAINT {{.Prefix}}fk_iam_invitation_roles_invitation
        FOREIGN KEY (tenant_id, invitation_id)
        REFERENCES {{.Prefix}}iam_invitations (tenant_id, id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_invitation_roles
SELECT * FROM {{.Prefix}}iam_invitation_roles_old;
DROP TABLE {{.Prefix}}iam_invitation_roles_old;

ALTER TABLE {{.Prefix}}iam_role_scope_departments
    RENAME TO {{.Prefix}}iam_role_scope_departments_old;
ALTER TABLE {{.Prefix}}iam_role_data_scopes
    RENAME TO {{.Prefix}}iam_role_data_scopes_old;
CREATE TABLE {{.Prefix}}iam_role_data_scopes (
    tenant_id  BIGINT   NOT NULL,
    role_id    BIGINT   NOT NULL,
    scope_type TEXT     COLLATE BINARY NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id),
    CONSTRAINT {{.Prefix}}chk_iam_role_data_scopes_type
        CHECK (scope_type IN ('all', 'self', 'department',
            'department_and_descendants', 'selected_departments'))
);
INSERT INTO {{.Prefix}}iam_role_data_scopes
SELECT * FROM {{.Prefix}}iam_role_data_scopes_old;
CREATE TABLE {{.Prefix}}iam_role_scope_departments (
    tenant_id     BIGINT   NOT NULL,
    role_id       BIGINT   NOT NULL,
    department_id BIGINT   NOT NULL,
    created_at    DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id, department_id),
    CONSTRAINT {{.Prefix}}fk_iam_role_scope_departments_scope
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_role_data_scopes (tenant_id, role_id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_role_scope_departments_department
        FOREIGN KEY (tenant_id, department_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_role_scope_departments
SELECT * FROM {{.Prefix}}iam_role_scope_departments_old;
DROP TABLE {{.Prefix}}iam_role_scope_departments_old;
DROP TABLE {{.Prefix}}iam_role_data_scopes_old;
CREATE INDEX {{.Prefix}}idx_iam_role_scope_departments_department
    ON {{.Prefix}}iam_role_scope_departments (tenant_id, department_id, role_id);

ALTER TABLE {{.Prefix}}iam_member_departments
    RENAME TO {{.Prefix}}iam_member_departments_old;
CREATE TABLE {{.Prefix}}iam_member_departments (
    tenant_id     BIGINT   NOT NULL,
    principal_id  BIGINT   NOT NULL,
    department_id BIGINT   NOT NULL,
    updated_at    DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_member_departments_department
        FOREIGN KEY (tenant_id, department_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_member_departments
SELECT * FROM {{.Prefix}}iam_member_departments_old;
DROP TABLE {{.Prefix}}iam_member_departments_old;
CREATE INDEX {{.Prefix}}idx_iam_member_departments_department
    ON {{.Prefix}}iam_member_departments (tenant_id, department_id, principal_id);

ALTER TABLE {{.Prefix}}iam_position_role_bindings
    RENAME TO {{.Prefix}}iam_position_role_bindings_old;
CREATE TABLE {{.Prefix}}iam_position_role_bindings (
    tenant_id  BIGINT   NOT NULL,
    role_id    BIGINT   NOT NULL,
    position_id BIGINT  NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id, position_id),
    CONSTRAINT {{.Prefix}}fk_iam_position_role_bindings_position
        FOREIGN KEY (tenant_id, position_id)
        REFERENCES {{.Prefix}}iam_positions (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_position_role_bindings
SELECT * FROM {{.Prefix}}iam_position_role_bindings_old;
DROP TABLE {{.Prefix}}iam_position_role_bindings_old;
CREATE INDEX {{.Prefix}}idx_iam_position_role_bindings_position
    ON {{.Prefix}}iam_position_role_bindings (tenant_id, position_id, role_id);

ALTER TABLE {{.Prefix}}iam_group_role_bindings
    RENAME TO {{.Prefix}}iam_group_role_bindings_old;
CREATE TABLE {{.Prefix}}iam_group_role_bindings (
    tenant_id  BIGINT   NOT NULL,
    role_id    BIGINT   NOT NULL,
    group_id   BIGINT   NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id, group_id),
    CONSTRAINT {{.Prefix}}fk_iam_group_role_bindings_group
        FOREIGN KEY (tenant_id, group_id)
        REFERENCES {{.Prefix}}iam_groups (tenant_id, id) ON DELETE RESTRICT
);
INSERT INTO {{.Prefix}}iam_group_role_bindings
SELECT * FROM {{.Prefix}}iam_group_role_bindings_old;
DROP TABLE {{.Prefix}}iam_group_role_bindings_old;
CREATE INDEX {{.Prefix}}idx_iam_group_role_bindings_group
    ON {{.Prefix}}iam_group_role_bindings (tenant_id, group_id, role_id);

ALTER TABLE {{.Prefix}}iam_group_members
    RENAME TO {{.Prefix}}iam_group_members_old;
CREATE TABLE {{.Prefix}}iam_group_members (
    tenant_id    BIGINT   NOT NULL,
    group_id     BIGINT   NOT NULL,
    principal_id BIGINT   NOT NULL,
    starts_at    DATETIME NULL,
    ends_at      DATETIME NULL,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, group_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_group_members_group
        FOREIGN KEY (tenant_id, group_id)
        REFERENCES {{.Prefix}}iam_groups (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_group_members_window
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
INSERT INTO {{.Prefix}}iam_group_members
SELECT * FROM {{.Prefix}}iam_group_members_old;
DROP TABLE {{.Prefix}}iam_group_members_old;
CREATE INDEX {{.Prefix}}idx_iam_group_members_principal
    ON {{.Prefix}}iam_group_members
        (tenant_id, principal_id, starts_at, ends_at, group_id);

ALTER TABLE {{.Prefix}}iam_position_members
    RENAME TO {{.Prefix}}iam_position_members_old;
CREATE TABLE {{.Prefix}}iam_position_members (
    tenant_id    BIGINT   NOT NULL,
    position_id  BIGINT   NOT NULL,
    principal_id BIGINT   NOT NULL,
    starts_at    DATETIME NULL,
    ends_at      DATETIME NULL,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, position_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_position_members_position
        FOREIGN KEY (tenant_id, position_id)
        REFERENCES {{.Prefix}}iam_positions (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_position_members_window
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
INSERT INTO {{.Prefix}}iam_position_members
SELECT * FROM {{.Prefix}}iam_position_members_old;
DROP TABLE {{.Prefix}}iam_position_members_old;
CREATE INDEX {{.Prefix}}idx_iam_position_members_principal
    ON {{.Prefix}}iam_position_members
        (tenant_id, principal_id, starts_at, ends_at, position_id);
