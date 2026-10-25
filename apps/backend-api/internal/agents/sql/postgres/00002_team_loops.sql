-- +goose Up
ALTER TABLE {{.Prefix}}agents_reminders ALTER COLUMN project_id DROP NOT NULL;
ALTER TABLE {{.Prefix}}agents_reminders ADD CONSTRAINT {{.Prefix}}chk_agents_reminder_project CHECK (project_id IS NULL OR project_id > 0);

-- +goose Down
ALTER TABLE {{.Prefix}}agents_reminders DROP CONSTRAINT {{.Prefix}}chk_agents_reminder_project;
ALTER TABLE {{.Prefix}}agents_reminders ALTER COLUMN project_id SET NOT NULL;
