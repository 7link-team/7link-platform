-- Source: internal/modules/organization/sql/postgres/00001_departments.sql:1-31
-- +goose Up
CREATE TABLE {{.Prefix}}iam_departments (
    tenant_id BIGINT NOT NULL,
    id BIGINT NOT NULL,
    parent_id BIGINT NOT NULL DEFAULT '',
    name VARCHAR(128) NOT NULL,
    name_key VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('active', 'disabled')),
    sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0 AND sort_order <= 1000000),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, parent_id, name_key)
);
CREATE INDEX {{.Prefix}}idx_iam_departments_parent ON {{.Prefix}}iam_departments (tenant_id, parent_id, sort_order, name_key, id);

CREATE TABLE {{.Prefix}}iam_department_closure (
    tenant_id BIGINT NOT NULL,
    ancestor_id BIGINT NOT NULL,
    descendant_id BIGINT NOT NULL,
    depth INTEGER NOT NULL CHECK (depth >= 0),
    PRIMARY KEY (tenant_id, ancestor_id, descendant_id),
    FOREIGN KEY (tenant_id, ancestor_id) REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, descendant_id) REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_iam_department_closure_descendant ON {{.Prefix}}iam_department_closure (tenant_id, descendant_id, depth, ancestor_id);

-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}iam_department_closure;
DROP TABLE IF EXISTS {{.Prefix}}iam_departments;