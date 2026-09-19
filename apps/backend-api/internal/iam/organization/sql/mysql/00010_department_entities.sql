-- Departments are structurally owned by one IAM entity. The column remains
-- nullable only so existing installations can repair legacy rows explicitly.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_departments
    ADD COLUMN entity_id BIGINT NULL AFTER id,
    ADD CONSTRAINT {{.Prefix}}fk_iam_departments_entity
        FOREIGN KEY (tenant_id, entity_id)
        REFERENCES {{.Prefix}}iam_entities (tenant_id, id) ON DELETE RESTRICT,
    ADD UNIQUE KEY {{.Prefix}}uq_iam_departments_id_entity (tenant_id, id, entity_id),
    ADD CONSTRAINT {{.Prefix}}fk_iam_departments_parent_entity
        FOREIGN KEY (tenant_id, parent_id, entity_id)
        REFERENCES {{.Prefix}}iam_departments (tenant_id, id, entity_id) ON DELETE RESTRICT,
    ADD INDEX {{.Prefix}}idx_iam_departments_entity_parent
        (tenant_id, entity_id, parent_id, sort_order, name_key, id);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_departments
    DROP FOREIGN KEY {{.Prefix}}fk_iam_departments_parent_entity,
    DROP FOREIGN KEY {{.Prefix}}fk_iam_departments_entity,
    DROP INDEX {{.Prefix}}idx_iam_departments_entity_parent,
    DROP INDEX {{.Prefix}}uq_iam_departments_id_entity,
    DROP COLUMN entity_id;
