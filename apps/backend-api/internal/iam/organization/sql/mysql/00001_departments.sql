-- Source: internal/modules/organization/sql/mysql/00001_departments.sql:1-35.
-- Validation: internal/modules/organization/domain.go:61-111.
-- MySQL DDL auto-commits; each base version is independently recoverable.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_departments (
    tenant_id BIGINT       NOT NULL,
    id        BIGINT       NOT NULL,
    parent_id BIGINT       NULL,
    parent_key BIGINT GENERATED ALWAYS AS (COALESCE(parent_id, 0)) STORED,
    name      VARCHAR(128) NOT NULL,
    name_key  VARCHAR(128) COLLATE utf8mb4_bin NOT NULL,
    status    VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL,
    sort_order INT         NOT NULL DEFAULT 0,
    version   BIGINT       NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_departments_sibling_name
        UNIQUE (tenant_id, parent_key, name_key),
    CONSTRAINT {{.Prefix}}fk_iam_departments_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_departments_id CHECK (id > 0),
    CONSTRAINT {{.Prefix}}chk_iam_departments_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_departments_sort_order
        CHECK (sort_order >= 0 AND sort_order <= 1000000),
    CONSTRAINT {{.Prefix}}chk_iam_departments_version CHECK (version >= 1),
    INDEX {{.Prefix}}idx_iam_departments_parent
        (tenant_id, parent_id, sort_order, name_key, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_department_closure (
    tenant_id     BIGINT NOT NULL,
    ancestor_id   BIGINT NOT NULL,
    descendant_id BIGINT NOT NULL,
    depth          INT    NOT NULL,
    PRIMARY KEY (tenant_id, ancestor_id, descendant_id),
    CONSTRAINT {{.Prefix}}fk_iam_department_closure_ancestor
        FOREIGN KEY (tenant_id, ancestor_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_department_closure_descendant
        FOREIGN KEY (tenant_id, descendant_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_department_closure_depth CHECK (depth >= 0),
    INDEX {{.Prefix}}idx_iam_department_closure_descendant
        (tenant_id, descendant_id, depth, ancestor_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_department_closure;
DROP TABLE {{.Prefix}}iam_departments;
