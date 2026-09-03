-- Source: internal/modules/organization/sql/sqlite/00006_iam_data_scopes.sql:1-36.
-- Cross-module member and role references are added by 00008_constraints.sql.
-- +goose Up
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
CREATE INDEX {{.Prefix}}idx_iam_member_departments_department
    ON {{.Prefix}}iam_member_departments (tenant_id, department_id, principal_id);

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
CREATE INDEX {{.Prefix}}idx_iam_role_scope_departments_department
    ON {{.Prefix}}iam_role_scope_departments (tenant_id, department_id, role_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_role_scope_departments;
DROP TABLE {{.Prefix}}iam_role_data_scopes;
DROP TABLE {{.Prefix}}iam_member_departments;
