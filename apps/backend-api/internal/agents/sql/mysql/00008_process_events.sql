-- +goose Up
CREATE TABLE {{.Prefix}}agents_process_events (
id BIGINT NOT NULL PRIMARY KEY,
tenant_id BIGINT NOT NULL,
agent_principal_id BIGINT NOT NULL,
kind VARCHAR(32) NOT NULL,
machine_id BIGINT NULL,
pid INT NOT NULL,
restart_count INT NOT NULL,
reason VARCHAR(2000) NOT NULL,
occurred_at DATETIME(3) NOT NULL,
CHECK (pid >= 0 AND restart_count BETWEEN 0 AND 4),
CHECK (kind IN ('START_REQUESTED','STOP_REQUESTED','RESTART_REQUESTED','STARTED','STOPPED','CRASHED','AUTO_RESTART_SCHEDULED','RESTART_EXHAUSTED'))
);
CREATE INDEX {{.Prefix}}agents_process_events_agent ON {{.Prefix}}agents_process_events (tenant_id, agent_principal_id, occurred_at, id);
-- +goose Down
DROP TABLE {{.Prefix}}agents_process_events;
