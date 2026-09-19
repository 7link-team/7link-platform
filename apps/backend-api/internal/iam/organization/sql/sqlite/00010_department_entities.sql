-- SQLite cannot add composite foreign keys to an existing table. These
-- triggers enforce the same tenant/entity and same-entity-parent invariants
-- while preserving nullable legacy rows for explicit repair.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_departments ADD COLUMN entity_id BIGINT NULL;

CREATE UNIQUE INDEX {{.Prefix}}uq_iam_departments_id_entity
    ON {{.Prefix}}iam_departments (tenant_id, id, entity_id);
CREATE INDEX {{.Prefix}}idx_iam_departments_entity_parent
    ON {{.Prefix}}iam_departments (tenant_id, entity_id, parent_id, sort_order, name_key, id);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}iam_departments_entity_i
BEFORE INSERT ON {{.Prefix}}iam_departments
WHEN NEW.entity_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'department entity does not exist')
        WHERE NOT EXISTS (
            SELECT 1 FROM {{.Prefix}}iam_entities
            WHERE tenant_id = NEW.tenant_id AND id = NEW.entity_id
        );
    SELECT RAISE(ABORT, 'department parent belongs to another entity')
        WHERE NEW.parent_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM {{.Prefix}}iam_departments
            WHERE tenant_id = NEW.tenant_id AND id = NEW.parent_id
              AND entity_id = NEW.entity_id
        );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}iam_departments_entity_u
BEFORE UPDATE OF tenant_id, entity_id, parent_id ON {{.Prefix}}iam_departments
WHEN NEW.entity_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'department entity does not exist')
        WHERE NOT EXISTS (
            SELECT 1 FROM {{.Prefix}}iam_entities
            WHERE tenant_id = NEW.tenant_id AND id = NEW.entity_id
        );
    SELECT RAISE(ABORT, 'department parent belongs to another entity')
        WHERE NEW.parent_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM {{.Prefix}}iam_departments
            WHERE tenant_id = NEW.tenant_id AND id = NEW.parent_id
              AND entity_id = NEW.entity_id
        );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}iam_entities_departments_d
BEFORE DELETE ON {{.Prefix}}iam_entities
BEGIN
    SELECT RAISE(ABORT, 'entity is referenced by departments')
        WHERE EXISTS (
            SELECT 1 FROM {{.Prefix}}iam_departments
            WHERE tenant_id = OLD.tenant_id AND entity_id = OLD.id
        );
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER {{.Prefix}}iam_entities_departments_d;
DROP TRIGGER {{.Prefix}}iam_departments_entity_u;
DROP TRIGGER {{.Prefix}}iam_departments_entity_i;
DROP INDEX {{.Prefix}}idx_iam_departments_entity_parent;
DROP INDEX {{.Prefix}}uq_iam_departments_id_entity;
ALTER TABLE {{.Prefix}}iam_departments DROP COLUMN entity_id;
