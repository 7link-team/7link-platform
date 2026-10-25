-- +goose Up
ALTER TABLE {{.Prefix}}fleet_machine_enrollments ADD COLUMN facts_json TEXT NULL;
CREATE INDEX {{.Prefix}}fleet_enrollment_code_lookup ON {{.Prefix}}fleet_machine_enrollments(tenant_id,user_hash,state,expires_at);
-- +goose Down
DROP INDEX {{.Prefix}}fleet_enrollment_code_lookup ON {{.Prefix}}fleet_machine_enrollments;
ALTER TABLE {{.Prefix}}fleet_machine_enrollments DROP COLUMN facts_json;
