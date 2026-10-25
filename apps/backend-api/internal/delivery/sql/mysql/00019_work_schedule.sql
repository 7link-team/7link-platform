-- +goose Up
ALTER TABLE {{.Prefix}}delivery_work_items ADD COLUMN scheduled_start DATETIME(3) NULL;
ALTER TABLE {{.Prefix}}delivery_work_items ADD COLUMN scheduled_end DATETIME(3) NULL;
ALTER TABLE {{.Prefix}}delivery_work_items ADD COLUMN schedule_timezone VARCHAR(80) NOT NULL DEFAULT 'UTC';
-- +goose Down
ALTER TABLE {{.Prefix}}delivery_work_items DROP COLUMN schedule_timezone;
ALTER TABLE {{.Prefix}}delivery_work_items DROP COLUMN scheduled_end;
ALTER TABLE {{.Prefix}}delivery_work_items DROP COLUMN scheduled_start;
