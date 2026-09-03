-- Sources: internal/modules/organization/sql/mysql/00003_groups.sql:1-42;
-- internal/modules/organization/sql/mysql/00008_dynamic_groups.sql:1-5.
-- Validation: internal/modules/organization/group.go:88-151.
-- MySQL DDL auto-commits; each base version is independently recoverable.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_groups (
    tenant_id  BIGINT       NOT NULL,
    id         BIGINT       NOT NULL,
    name       VARCHAR(128) NOT NULL,
    name_key   VARCHAR(128) COLLATE utf8mb4_bin NOT NULL,
    group_type VARCHAR(32)  COLLATE utf8mb4_bin NOT NULL,
    rule_json  TEXT         NOT NULL DEFAULT (''),
    description TEXT        NOT NULL DEFAULT (''),
    status     VARCHAR(32)  COLLATE utf8mb4_bin NOT NULL,
    sort_order INT          NOT NULL DEFAULT 0,
    version    BIGINT       NOT NULL DEFAULT 1,
    created_at DATETIME(3)  NOT NULL,
    updated_at DATETIME(3)  NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_groups_name UNIQUE (tenant_id, name_key),
    CONSTRAINT {{.Prefix}}chk_iam_groups_type
        CHECK (group_type IN ('static', 'dynamic')),
    CONSTRAINT {{.Prefix}}chk_iam_groups_rule
        CHECK (OCTET_LENGTH(rule_json) <= 4096),
    CONSTRAINT {{.Prefix}}chk_iam_groups_description
        CHECK (OCTET_LENGTH(description) <= 1024),
    CONSTRAINT {{.Prefix}}chk_iam_groups_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_groups_sort_order
        CHECK (sort_order >= 0 AND sort_order <= 1000000),
    CONSTRAINT {{.Prefix}}chk_iam_groups_version CHECK (version >= 1),
    INDEX {{.Prefix}}idx_iam_groups_order (tenant_id, sort_order, name_key, id),
    INDEX {{.Prefix}}idx_iam_groups_dynamic (tenant_id, group_type, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_group_members (
    tenant_id    BIGINT      NOT NULL,
    group_id     BIGINT      NOT NULL,
    principal_id BIGINT      NOT NULL,
    starts_at    DATETIME(3) NULL,
    ends_at      DATETIME(3) NULL,
    created_at   DATETIME(3) NOT NULL,
    updated_at   DATETIME(3) NOT NULL,
    PRIMARY KEY (tenant_id, group_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_group_members_group
        FOREIGN KEY (tenant_id, group_id)
        REFERENCES {{.Prefix}}iam_groups (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_group_members_window
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at),
    INDEX {{.Prefix}}idx_iam_group_members_principal
        (tenant_id, principal_id, starts_at, ends_at, group_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_group_members;
DROP TABLE {{.Prefix}}iam_groups;
