-- +goose Up
ALTER TABLE {{.Prefix}}fleet_machine_enrollments ADD COLUMN poll_interval_seconds BIGINT NOT NULL DEFAULT 5;
ALTER TABLE {{.Prefix}}fleet_machine_enrollments ADD COLUMN last_polled_at DATETIME(3) NULL;
ALTER TABLE {{.Prefix}}fleet_machine_enrollments ADD CONSTRAINT {{.Prefix}}fleet_enrollment_poll_interval CHECK (poll_interval_seconds BETWEEN 1 AND 300);
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_machine_enrollments DROP CHECK {{.Prefix}}fleet_enrollment_poll_interval;
ALTER TABLE {{.Prefix}}fleet_machine_enrollments DROP COLUMN last_polled_at;
ALTER TABLE {{.Prefix}}fleet_machine_enrollments DROP COLUMN poll_interval_seconds;
