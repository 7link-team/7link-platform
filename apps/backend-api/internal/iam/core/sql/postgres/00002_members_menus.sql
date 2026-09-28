-- Source: internal/modules/iam/sql/postgres/00002_iam_members_menus.sql:2-31.
-- Validation: internal/modules/iam/service_admin.go:14-18,268-289.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_tenant_members (
    tenant_id    BIGINT         NOT NULL,
    principal_id BIGINT         NOT NULL,
    display_name VARCHAR(128)   NOT NULL DEFAULT '',
    email        VARCHAR(320)   COLLATE "C" NOT NULL DEFAULT '',
    status       VARCHAR(16)    COLLATE "C" NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    updated_at   TIMESTAMPTZ(3) NOT NULL,
    disabled_at  TIMESTAMPTZ(3) NULL,
    PRIMARY KEY (tenant_id, principal_id),
    CONSTRAINT {{.Prefix}}chk_iam_tenant_members_status
        CHECK (status IN ('active', 'disabled'))
);
CREATE INDEX {{.Prefix}}idx_iam_tenant_members_status
    ON {{.Prefix}}iam_tenant_members (tenant_id, status, display_name, principal_id);

CREATE TABLE {{.Prefix}}iam_menus (
    tenant_id      BIGINT         NOT NULL,
    id             BIGINT         NOT NULL,
    parent_id      BIGINT         NULL,
    label          VARCHAR(128)   NOT NULL,
    route          VARCHAR(512)   COLLATE "C" NULL,
    icon           VARCHAR(64)    COLLATE "C" NOT NULL DEFAULT '',
    sort_order     INTEGER        NOT NULL DEFAULT 0,
    permission_code VARCHAR(128)  COLLATE "C" NOT NULL DEFAULT '',
    status         VARCHAR(16)    COLLATE "C" NOT NULL,
    created_at     TIMESTAMPTZ(3) NOT NULL,
    updated_at     TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_menus_route UNIQUE (tenant_id, route),
    CONSTRAINT {{.Prefix}}fk_iam_menus_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_menus (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_menus_status
        CHECK (status IN ('active', 'disabled'))
);
CREATE INDEX {{.Prefix}}idx_iam_menus_tree
    ON {{.Prefix}}iam_menus (tenant_id, status, parent_id, sort_order, id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_menus;
DROP TABLE {{.Prefix}}iam_tenant_members;
