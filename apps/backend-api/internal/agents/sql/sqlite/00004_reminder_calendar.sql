-- +goose Up
ALTER TABLE {{.Prefix}}agents_reminders ADD COLUMN anchor_kind VARCHAR(24) NOT NULL DEFAULT 'THREAD';
ALTER TABLE {{.Prefix}}agents_reminders ADD COLUMN work_item_id BIGINT NULL;
ALTER TABLE {{.Prefix}}agents_reminders ADD COLUMN schedule_kind VARCHAR(16) NOT NULL DEFAULT 'ONCE';
ALTER TABLE {{.Prefix}}agents_reminders ADD COLUMN period_kind VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}agents_reminders ADD COLUMN timezone VARCHAR(64) NOT NULL DEFAULT '';
UPDATE {{.Prefix}}agents_reminders SET schedule_kind = 'INTERVAL' WHERE interval_minutes > 0;

-- +goose Down
-- New anchor/calendar semantics cannot be represented by the prior schema.
-- A failed MySQL DDL transaction may retain this empty probe; retry is safe.
CREATE TABLE IF NOT EXISTS {{.Prefix}}agents_reminder_downgrade_guard (blocked INT NOT NULL CHECK (blocked = 0));
INSERT INTO {{.Prefix}}agents_reminder_downgrade_guard (blocked)
SELECT 1 FROM {{.Prefix}}agents_reminders WHERE anchor_kind = 'WORK_ITEM' OR schedule_kind = 'PERIOD' LIMIT 1;
DROP TABLE {{.Prefix}}agents_reminder_downgrade_guard;
ALTER TABLE {{.Prefix}}agents_reminders DROP COLUMN timezone;
ALTER TABLE {{.Prefix}}agents_reminders DROP COLUMN period_kind;
ALTER TABLE {{.Prefix}}agents_reminders DROP COLUMN schedule_kind;
ALTER TABLE {{.Prefix}}agents_reminders DROP COLUMN work_item_id;
ALTER TABLE {{.Prefix}}agents_reminders DROP COLUMN anchor_kind;
