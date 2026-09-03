-- Source: internal/modules/organization/sql/postgres/00001_departments.sql:1-31.
-- Validation: internal/modules/organization/domain.go:61-111.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_departments (
    tenant_id BIGINT         NOT NULL,
    id        BIGINT         NOT NULL,
    parent_id BIGINT         NULL,
    name      VARCHAR(128)   NOT NULL,
    name_key  VARCHAR(128)   COLLATE "C" NOT NULL,
    status    VARCHAR(16)    COLLATE "C" NOT NULL,
    sort_order INTEGER       NOT NULL DEFAULT 0,
    version   BIGINT         NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}fk_iam_departments_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_departments_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_departments_sort_order
        CHECK (sort_order >= 0 AND sort_order <= 1000000),
    CONSTRAINT {{.Prefix}}chk_iam_departments_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX {{.Prefix}}uq_iam_departments_root_name
    ON {{.Prefix}}iam_departments (tenant_id, name_key)
    WHERE parent_id IS NULL;
CREATE UNIQUE INDEX {{.Prefix}}uq_iam_departments_child_name
    ON {{.Prefix}}iam_departments (tenant_id, parent_id, name_key)
    WHERE parent_id IS NOT NULL;
CREATE INDEX {{.Prefix}}idx_iam_departments_parent
    ON {{.Prefix}}iam_departments (tenant_id, parent_id, sort_order, name_key, id);

CREATE TABLE {{.Prefix}}iam_department_closure (
    tenant_id     BIGINT  NOT NULL,
    ancestor_id   BIGINT  NOT NULL,
    descendant_id BIGINT  NOT NULL,
    depth          INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, ancestor_id, descendant_id),
    CONSTRAINT {{.Prefix}}fk_iam_department_closure_ancestor
        FOREIGN KEY (tenant_id, ancestor_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_department_closure_descendant
        FOREIGN KEY (tenant_id, descendant_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_department_closure_depth CHECK (depth >= 0)
);
CREATE INDEX {{.Prefix}}idx_iam_department_closure_descendant
    ON {{.Prefix}}iam_department_closure (tenant_id, descendant_id, depth, ancestor_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_department_closure;
DROP TABLE {{.Prefix}}iam_departments;
