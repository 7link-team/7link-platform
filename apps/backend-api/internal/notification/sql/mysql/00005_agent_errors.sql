-- +goose Up
CREATE TABLE {{.Prefix}}notification_agent_errors(tenant_id BIGINT NOT NULL, agent_id BIGINT NOT NULL, entry_id BIGINT NOT NULL, session_epoch INTEGER NOT NULL, entered_at DATETIME(3) NOT NULL, active BOOLEAN NOT NULL, notified BOOLEAN NOT NULL, PRIMARY KEY(tenant_id,agent_id));
-- +goose Down
DROP TABLE {{.Prefix}}notification_agent_errors;
