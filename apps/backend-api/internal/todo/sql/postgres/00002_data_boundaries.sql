-- +goose Up
-- Legacy rows remain unassigned and cannot match an authorized boundary.
ALTER TABLE {{.Prefix}}todos ADD COLUMN tenant_id BIGINT NULL CHECK (tenant_id > 0);
ALTER TABLE {{.Prefix}}todos ADD COLUMN entity_id BIGINT NULL CHECK (entity_id > 0);
ALTER TABLE {{.Prefix}}todos ADD COLUMN department_id BIGINT NULL CHECK (department_id > 0);
CREATE INDEX {{.Prefix}}idx_todos_boundary_created ON {{.Prefix}}todos (tenant_id, entity_id, deleted_at, created_at, id);
CREATE INDEX {{.Prefix}}idx_todos_boundary_owner ON {{.Prefix}}todos (tenant_id, entity_id, created_by, deleted_at);
CREATE INDEX {{.Prefix}}idx_todos_boundary_department ON {{.Prefix}}todos (tenant_id, entity_id, department_id, deleted_at);

-- +goose Down
DROP INDEX {{.Prefix}}idx_todos_boundary_department;
DROP INDEX {{.Prefix}}idx_todos_boundary_owner;
DROP INDEX {{.Prefix}}idx_todos_boundary_created;
ALTER TABLE {{.Prefix}}todos DROP COLUMN department_id;
ALTER TABLE {{.Prefix}}todos DROP COLUMN entity_id;
ALTER TABLE {{.Prefix}}todos DROP COLUMN tenant_id;
