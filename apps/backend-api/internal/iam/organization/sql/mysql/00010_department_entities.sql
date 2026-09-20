-- Departments are structurally owned by one IAM entity. The column remains
-- nullable only so existing installations can repair legacy rows explicitly.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_departments
    ADD COLUMN entity_id BIGINT NULL AFTER id;

ALTER TABLE {{.Prefix}}iam_departments
    ADD CONSTRAINT {{.Prefix}}fk_iam_departments_entity
        FOREIGN KEY (tenant_id, entity_id)
        REFERENCES {{.Prefix}}iam_entities (tenant_id, id) ON DELETE RESTRICT;

ALTER TABLE {{.Prefix}}iam_departments
    ADD UNIQUE KEY {{.Prefix}}uq_iam_departments_id_entity (tenant_id, id, entity_id);

ALTER TABLE {{.Prefix}}iam_departments
    ADD CONSTRAINT {{.Prefix}}fk_iam_departments_parent_entity
        FOREIGN KEY (tenant_id, parent_id, entity_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id, entity_id) ON DELETE RESTRICT;

ALTER TABLE {{.Prefix}}iam_departments
    ADD INDEX {{.Prefix}}idx_iam_departments_entity_parent
        (tenant_id, entity_id, parent_id, sort_order, name_key, id);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_member_departments
    DROP FOREIGN KEY {{.Prefix}}fk_iam_member_departments_department;

ALTER TABLE {{.Prefix}}iam_role_scope_departments
    DROP FOREIGN KEY {{.Prefix}}fk_iam_role_scope_departments_department;

ALTER TABLE {{.Prefix}}iam_department_closure
    DROP FOREIGN KEY {{.Prefix}}fk_iam_department_closure_ancestor,
    DROP FOREIGN KEY {{.Prefix}}fk_iam_department_closure_descendant;

ALTER TABLE {{.Prefix}}iam_departments
    DROP FOREIGN KEY {{.Prefix}}fk_iam_departments_parent_entity,
    DROP FOREIGN KEY {{.Prefix}}fk_iam_departments_entity,
    DROP FOREIGN KEY {{.Prefix}}fk_iam_departments_parent;

ALTER TABLE {{.Prefix}}iam_departments
    DROP INDEX {{.Prefix}}idx_iam_departments_entity_parent,
    DROP INDEX {{.Prefix}}uq_iam_departments_id_entity,
    DROP COLUMN entity_id;

ALTER TABLE {{.Prefix}}iam_departments
    ADD CONSTRAINT {{.Prefix}}fk_iam_departments_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT;

ALTER TABLE {{.Prefix}}iam_department_closure
    ADD CONSTRAINT {{.Prefix}}fk_iam_department_closure_ancestor
        FOREIGN KEY (tenant_id, ancestor_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT {{.Prefix}}fk_iam_department_closure_descendant
        FOREIGN KEY (tenant_id, descendant_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE CASCADE;

ALTER TABLE {{.Prefix}}iam_member_departments
    ADD CONSTRAINT {{.Prefix}}fk_iam_member_departments_department
        FOREIGN KEY (tenant_id, department_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT;

ALTER TABLE {{.Prefix}}iam_role_scope_departments
    ADD CONSTRAINT {{.Prefix}}fk_iam_role_scope_departments_department
        FOREIGN KEY (tenant_id, department_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id) ON DELETE RESTRICT;
