-- Sources: internal/modules/organization/sql/postgres/00003_groups.sql:1-36;
-- internal/modules/organization/sql/postgres/00008_dynamic_groups.sql:1-5.
-- Validation: internal/modules/organization/group.go:88-151.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_groups (
    tenant_id  BIGINT         NOT NULL,
    id         BIGINT         NOT NULL,
    name       VARCHAR(128)   NOT NULL,
    name_key   VARCHAR(128)   COLLATE "C" NOT NULL,
    group_type VARCHAR(32)    COLLATE "C" NOT NULL,
    rule_json  TEXT           NOT NULL DEFAULT '',
    description TEXT          NOT NULL DEFAULT '',
    status     VARCHAR(32)    COLLATE "C" NOT NULL,
    sort_order INTEGER        NOT NULL DEFAULT 0,
    version    BIGINT         NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_groups_name UNIQUE (tenant_id, name_key),
    CONSTRAINT {{.Prefix}}chk_iam_groups_type
        CHECK (group_type IN ('static', 'dynamic')),
    CONSTRAINT {{.Prefix}}chk_iam_groups_rule
        CHECK (octet_length(rule_json) <= 4096),
    CONSTRAINT {{.Prefix}}chk_iam_groups_description
        CHECK (octet_length(description) <= 1024),
    CONSTRAINT {{.Prefix}}chk_iam_groups_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_groups_sort_order
        CHECK (sort_order >= 0 AND sort_order <= 1000000),
    CONSTRAINT {{.Prefix}}chk_iam_groups_version CHECK (version >= 1)
);
CREATE INDEX {{.Prefix}}idx_iam_groups_order
    ON {{.Prefix}}iam_groups (tenant_id, sort_order, name_key, id);
CREATE INDEX {{.Prefix}}idx_iam_groups_dynamic
    ON {{.Prefix}}iam_groups (tenant_id, group_type, status)
    WHERE group_type = 'dynamic';

CREATE TABLE {{.Prefix}}iam_group_members (
    tenant_id    BIGINT         NOT NULL,
    group_id     BIGINT         NOT NULL,
    principal_id BIGINT         NOT NULL,
    starts_at    TIMESTAMPTZ(3) NULL,
    ends_at      TIMESTAMPTZ(3) NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    updated_at   TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, group_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_group_members_group
        FOREIGN KEY (tenant_id, group_id)
        REFERENCES {{.Prefix}}iam_groups (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_group_members_window
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX {{.Prefix}}idx_iam_group_members_principal
    ON {{.Prefix}}iam_group_members
        (tenant_id, principal_id, starts_at, ends_at, group_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_group_members;
DROP TABLE {{.Prefix}}iam_groups;
