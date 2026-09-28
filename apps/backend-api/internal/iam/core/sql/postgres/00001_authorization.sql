-- Source: internal/modules/iam/sql/postgres/00001_iam_authorization.sql:2-29.
-- Condition source: internal/modules/iam/sql/postgres/00021_role_permission_conditions.sql:2.
-- Validation: internal/modules/iam/service.go:100-105,134-155;
-- internal/modules/iam/authorizer.go:88,607-611.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_roles (
    tenant_id  BIGINT         NOT NULL,
    id         BIGINT         NOT NULL,
    name       VARCHAR(128)   NOT NULL,
    name_key   VARCHAR(128)   COLLATE "C" NOT NULL,
    description TEXT          NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_roles_name UNIQUE (tenant_id, name_key)
);

CREATE TABLE {{.Prefix}}iam_role_permissions (
    tenant_id       BIGINT         NOT NULL,
    role_id         BIGINT         NOT NULL,
    permission_code VARCHAR(128)   COLLATE "C" NOT NULL,
    condition_json  TEXT           NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, role_id, permission_code),
    CONSTRAINT {{.Prefix}}fk_iam_role_permissions_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE {{.Prefix}}iam_role_members (
    tenant_id    BIGINT         NOT NULL,
    role_id      BIGINT         NOT NULL,
    principal_id BIGINT         NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, role_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_role_members_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_role_members;
DROP TABLE {{.Prefix}}iam_role_permissions;
DROP TABLE {{.Prefix}}iam_roles;
