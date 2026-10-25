-- +goose Up
CREATE TABLE {{.Prefix}}agents_reminders_team_tmp (
id BIGINT NOT NULL PRIMARY KEY,
tenant_id BIGINT NOT NULL,
agent_principal_id BIGINT NOT NULL,
thread_id BIGINT NOT NULL,
project_id BIGINT NULL CONSTRAINT {{.Prefix}}chk_agents_reminder_project CHECK (project_id IS NULL OR project_id > 0),
title VARCHAR(200) NOT NULL,
note VARCHAR(2000) NOT NULL,
interval_minutes INT NOT NULL,
next_fire_at DATETIME NULL,
fire_count INT NOT NULL,
max_fires INT NOT NULL,
state VARCHAR(16) NOT NULL,
checks VARCHAR(2000) NOT NULL,
budget_minor BIGINT NOT NULL,
escalation VARCHAR(2000) NOT NULL,
output_format VARCHAR(2000) NOT NULL,
version INT NOT NULL,
created_at DATETIME NOT NULL,
CHECK (state IN ('ACTIVE','PAUSED','DONE','CANCELLED')),
CHECK (budget_minor >= 0 AND fire_count >= 0 AND max_fires BETWEEN 1 AND 1000),
CHECK (interval_minutes = 0 OR interval_minutes BETWEEN 15 AND 10080)
);
INSERT INTO {{.Prefix}}agents_reminders_team_tmp (id, tenant_id, agent_principal_id, thread_id, project_id, title, note, interval_minutes, next_fire_at, fire_count, max_fires, state, checks, budget_minor, escalation, output_format, version, created_at) SELECT id, tenant_id, agent_principal_id, thread_id, project_id, title, note, interval_minutes, next_fire_at, fire_count, max_fires, state, checks, budget_minor, escalation, output_format, version, created_at FROM {{.Prefix}}agents_reminders;
DROP TABLE {{.Prefix}}agents_reminders;
ALTER TABLE {{.Prefix}}agents_reminders_team_tmp RENAME TO {{.Prefix}}agents_reminders;
CREATE INDEX {{.Prefix}}agents_reminders_due ON {{.Prefix}}agents_reminders (state, next_fire_at);

-- +goose Down
CREATE TABLE {{.Prefix}}agents_reminders_team_tmp (
id BIGINT NOT NULL PRIMARY KEY,
tenant_id BIGINT NOT NULL,
agent_principal_id BIGINT NOT NULL,
thread_id BIGINT NOT NULL,
project_id BIGINT NOT NULL,
title VARCHAR(200) NOT NULL,
note VARCHAR(2000) NOT NULL,
interval_minutes INT NOT NULL,
next_fire_at DATETIME NULL,
fire_count INT NOT NULL,
max_fires INT NOT NULL,
state VARCHAR(16) NOT NULL,
checks VARCHAR(2000) NOT NULL,
budget_minor BIGINT NOT NULL,
escalation VARCHAR(2000) NOT NULL,
output_format VARCHAR(2000) NOT NULL,
version INT NOT NULL,
created_at DATETIME NOT NULL,
CHECK (state IN ('ACTIVE','PAUSED','DONE','CANCELLED')),
CHECK (budget_minor >= 0 AND fire_count >= 0 AND max_fires BETWEEN 1 AND 1000),
CHECK (interval_minutes = 0 OR interval_minutes BETWEEN 15 AND 10080)
);
INSERT INTO {{.Prefix}}agents_reminders_team_tmp (id, tenant_id, agent_principal_id, thread_id, project_id, title, note, interval_minutes, next_fire_at, fire_count, max_fires, state, checks, budget_minor, escalation, output_format, version, created_at) SELECT id, tenant_id, agent_principal_id, thread_id, project_id, title, note, interval_minutes, next_fire_at, fire_count, max_fires, state, checks, budget_minor, escalation, output_format, version, created_at FROM {{.Prefix}}agents_reminders;
DROP TABLE {{.Prefix}}agents_reminders;
ALTER TABLE {{.Prefix}}agents_reminders_team_tmp RENAME TO {{.Prefix}}agents_reminders;
CREATE INDEX {{.Prefix}}agents_reminders_due ON {{.Prefix}}agents_reminders (state, next_fire_at);
