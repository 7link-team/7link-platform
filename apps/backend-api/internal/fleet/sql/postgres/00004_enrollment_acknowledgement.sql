-- +goose Up
ALTER TABLE {{.Prefix}}fleet_machine_enrollments ADD COLUMN acknowledged_at TIMESTAMPTZ(3) NULL;
CREATE INDEX {{.Prefix}}fleet_enrollments_cleanup ON {{.Prefix}}fleet_machine_enrollments(tenant_id,acknowledged_at,state,id);
-- +goose Down
DROP INDEX {{.Prefix}}fleet_enrollments_cleanup;
ALTER TABLE {{.Prefix}}fleet_machine_enrollments DROP COLUMN acknowledged_at;
