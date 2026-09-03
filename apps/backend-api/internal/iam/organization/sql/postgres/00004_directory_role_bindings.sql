-- Source: internal/modules/organization/sql/postgres/00004_directory_role_bindings.sql:1-26.
-- Cross-module role references are added by 00008_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_group_role_bindings (
    tenant_id  BIGINT         NOT NULL,
    role_id    BIGINT         NOT NULL,
    group_id   BIGINT         NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, role_id, group_id),
    CONSTRAINT {{.Prefix}}fk_iam_group_role_bindings_group
        FOREIGN KEY (tenant_id, group_id)
        REFERENCES {{.Prefix}}iam_groups (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_iam_group_role_bindings_group
    ON {{.Prefix}}iam_group_role_bindings (tenant_id, group_id, role_id);

CREATE TABLE {{.Prefix}}iam_position_role_bindings (
    tenant_id  BIGINT         NOT NULL,
    role_id    BIGINT         NOT NULL,
    position_id BIGINT        NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, role_id, position_id),
    CONSTRAINT {{.Prefix}}fk_iam_position_role_bindings_position
        FOREIGN KEY (tenant_id, position_id)
        REFERENCES {{.Prefix}}iam_positions (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_iam_position_role_bindings_position
    ON {{.Prefix}}iam_position_role_bindings (tenant_id, position_id, role_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_position_role_bindings;
DROP TABLE {{.Prefix}}iam_group_role_bindings;
