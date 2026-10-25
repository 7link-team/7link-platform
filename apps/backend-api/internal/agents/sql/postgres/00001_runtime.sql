-- +goose Up
CREATE TABLE {{.Prefix}}agents_live_states (
agent_principal_id BIGINT PRIMARY KEY,
tenant_id BIGINT NOT NULL,
head BIGINT NOT NULL,
ack BIGINT NOT NULL,
activity VARCHAR(16) NOT NULL,
session_epoch INT NOT NULL,
workspace_epoch INT NOT NULL,
updated_at TIMESTAMPTZ(3) NOT NULL,
CHECK (ack >= 0 AND ack <= head),
CHECK (session_epoch >= 1 AND workspace_epoch >= 1),
CHECK (activity IN ('ONLINE','THINKING','WORKING','ERROR','OFFLINE'))
);
CREATE TABLE {{.Prefix}}agents_inbox_events (
id BIGINT PRIMARY KEY,
tenant_id BIGINT NOT NULL,
agent_principal_id BIGINT NOT NULL,
seq BIGINT NOT NULL,
source_event_id VARCHAR(64) NOT NULL,
kind VARCHAR(32) NOT NULL,
subject_id BIGINT NOT NULL,
stream_key VARCHAR(64) NOT NULL,
stream_seq BIGINT NOT NULL,
created_at TIMESTAMPTZ(3) NOT NULL,
UNIQUE (agent_principal_id, seq),
UNIQUE (agent_principal_id, source_event_id),
CHECK (seq > 0 AND stream_seq >= 0)
);
CREATE TABLE {{.Prefix}}agents_resets (
id BIGINT PRIMARY KEY,
tenant_id BIGINT NOT NULL,
agent_principal_id BIGINT NOT NULL,
kind VARCHAR(16) NOT NULL,
state VARCHAR(16) NOT NULL,
reason VARCHAR(2000) NOT NULL,
requested_by BIGINT NOT NULL,
created_at TIMESTAMPTZ(3) NOT NULL,
CHECK (kind IN ('RESTART','SESSION','FULL')),
CHECK (state IN ('REQUESTED','DONE','FAILED','TIMED_OUT'))
);
CREATE TABLE {{.Prefix}}agents_reminders (
id BIGINT PRIMARY KEY,
tenant_id BIGINT NOT NULL,
agent_principal_id BIGINT NOT NULL,
thread_id BIGINT NOT NULL,
project_id BIGINT NOT NULL,
title VARCHAR(200) NOT NULL,
note VARCHAR(2000) NOT NULL,
interval_minutes INT NOT NULL,
next_fire_at TIMESTAMPTZ(3) NULL,
fire_count INT NOT NULL,
max_fires INT NOT NULL,
state VARCHAR(16) NOT NULL,
checks VARCHAR(2000) NOT NULL,
budget_minor BIGINT NOT NULL,
escalation VARCHAR(2000) NOT NULL,
output_format VARCHAR(2000) NOT NULL,
version INT NOT NULL,
created_at TIMESTAMPTZ(3) NOT NULL,
CHECK (state IN ('ACTIVE','PAUSED','DONE','CANCELLED')),
CHECK (budget_minor >= 0 AND fire_count >= 0 AND max_fires BETWEEN 1 AND 1000),
CHECK (interval_minutes = 0 OR interval_minutes BETWEEN 15 AND 10080)
);
CREATE TABLE {{.Prefix}}agents_tool_entries (
id BIGINT PRIMARY KEY,
tenant_id BIGINT NOT NULL,
entry_key VARCHAR(64) NOT NULL,
kind VARCHAR(16) NOT NULL,
title VARCHAR(200) NOT NULL,
transport VARCHAR(32) NOT NULL,
endpoint VARCHAR(512) NOT NULL,
content_hash VARCHAR(64) NOT NULL,
skill_body TEXT NOT NULL,
state VARCHAR(16) NOT NULL,
credential_sealed TEXT NOT NULL,
version INT NOT NULL,
created_at TIMESTAMPTZ(3) NOT NULL,
UNIQUE (tenant_id, entry_key),
CHECK (kind IN ('MCP_SERVER','SKILL')),
CHECK (state IN ('ACTIVE','DISABLED','ARCHIVED'))
);
CREATE TABLE {{.Prefix}}agents_tool_grants (
id BIGINT PRIMARY KEY,
tenant_id BIGINT NOT NULL,
tool_id BIGINT NOT NULL,
agent_principal_id BIGINT NOT NULL,
state VARCHAR(16) NOT NULL,
version INT NOT NULL,
created_at TIMESTAMPTZ(3) NOT NULL,
UNIQUE (tenant_id, tool_id, agent_principal_id),
CHECK (state IN ('ACTIVE','REVOKED'))
);
CREATE INDEX {{.Prefix}}agents_reminders_due ON {{.Prefix}}agents_reminders (state, next_fire_at);
-- +goose Down
DROP TABLE {{.Prefix}}agents_tool_grants;
DROP TABLE {{.Prefix}}agents_tool_entries;
DROP TABLE {{.Prefix}}agents_reminders;
DROP TABLE {{.Prefix}}agents_resets;
DROP TABLE {{.Prefix}}agents_inbox_events;
DROP TABLE {{.Prefix}}agents_live_states;
