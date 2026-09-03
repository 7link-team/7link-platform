-- Source: internal/modules/organization/sql/mysql/00004_directory_role_bindings.sql:1-26
-- +goose Up
CREATE TABLE {{.Prefix}}iam_group_role_bindings (
    tenant_id BIGINT NOT NULL,
    role_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (tenant_id, role_id, group_id),
    KEY {{.Prefix}}idx_iam_group_role_bindings_group (tenant_id, group_id, role_id),
    CONSTRAINT {{.Prefix}}fk_iam_group_role_bindings_role FOREIGN KEY (tenant_id, role_id) REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_group_role_bindings_group FOREIGN KEY (tenant_id, group_id) REFERENCES {{.Prefix}}iam_groups (tenant_id, id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_position_role_bindings (
    tenant_id BIGINT NOT NULL,
    role_id BIGINT NOT NULL,
    position_id BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (tenant_id, role_id, position_id),
    KEY {{.Prefix}}idx_iam_position_role_bindings_position (tenant_id, position_id, role_id),
    CONSTRAINT {{.Prefix}}fk_iam_position_role_bindings_role FOREIGN KEY (tenant_id, role_id) REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_position_role_bindings_position FOREIGN KEY (tenant_id, position_id) REFERENCES {{.Prefix}}iam_positions (tenant_id, id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}iam_position_role_bindings;
DROP TABLE IF EXISTS {{.Prefix}}iam_group_role_bindings;