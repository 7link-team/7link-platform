-- Source: internal/modules/iam/sql/mysql/00002_iam_members_menus.sql:2-32.
-- Validation: internal/modules/iam/service_admin.go:14-18,268-289.
-- MySQL DDL auto-commits; recover a partial application using the migration runbook.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_tenant_members (
    tenant_id    BIGINT       NOT NULL,
    principal_id BIGINT       NOT NULL,
    display_name VARCHAR(128) NOT NULL DEFAULT '',
    email        VARCHAR(320) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    status       VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL,
    created_at   DATETIME(3)  NOT NULL,
    updated_at   DATETIME(3)  NOT NULL,
    disabled_at  DATETIME(3)  NULL,
    PRIMARY KEY (tenant_id, principal_id),
    CONSTRAINT {{.Prefix}}chk_iam_tenant_members_status
        CHECK (status IN ('active', 'disabled')),
    INDEX {{.Prefix}}idx_iam_tenant_members_status
        (tenant_id, status, display_name, principal_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_menus (
    tenant_id       BIGINT       NOT NULL,
    id              BIGINT       NOT NULL,
    parent_id       BIGINT       NULL,
    label           VARCHAR(128) NOT NULL,
    route           VARCHAR(512) COLLATE utf8mb4_bin NULL,
    icon            VARCHAR(64)  COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    sort_order      INT          NOT NULL DEFAULT 0,
    permission_code VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    status          VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL,
    created_at      DATETIME(3)  NOT NULL,
    updated_at      DATETIME(3)  NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_menus_route UNIQUE (tenant_id, route),
    CONSTRAINT {{.Prefix}}fk_iam_menus_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_menus (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_menus_status
        CHECK (status IN ('active', 'disabled')),
    INDEX {{.Prefix}}idx_iam_menus_tree
        (tenant_id, status, parent_id, sort_order, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_menus;
DROP TABLE {{.Prefix}}iam_tenant_members;
