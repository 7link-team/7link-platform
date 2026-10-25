-- +goose Up
ALTER TABLE {{.Prefix}}fleet_machine_enrollments ADD COLUMN poll_interval_seconds BIGINT NOT NULL DEFAULT 5 CHECK (poll_interval_seconds BETWEEN 1 AND 300);
ALTER TABLE {{.Prefix}}fleet_machine_enrollments ADD COLUMN last_polled_at TIMESTAMPTZ(3) NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_machine_enrollments DROP COLUMN last_polled_at;
ALTER TABLE {{.Prefix}}fleet_machine_enrollments DROP COLUMN poll_interval_seconds;
