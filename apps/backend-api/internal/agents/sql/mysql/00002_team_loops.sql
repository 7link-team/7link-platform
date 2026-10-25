-- +goose Up
ALTER TABLE {{.Prefix}}agents_reminders MODIFY COLUMN project_id BIGINT NULL;
ALTER TABLE {{.Prefix}}agents_reminders ADD CONSTRAINT {{.Prefix}}chk_agents_reminder_project CHECK (project_id IS NULL OR project_id > 0);

-- +goose Down
ALTER TABLE {{.Prefix}}agents_reminders DROP CHECK {{.Prefix}}chk_agents_reminder_project;
ALTER TABLE {{.Prefix}}agents_reminders MODIFY COLUMN project_id BIGINT NOT NULL;
