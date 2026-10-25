-- +goose Up
CREATE TABLE {{.Prefix}}agents_retirements (
id BIGINT NOT NULL PRIMARY KEY,
tenant_id BIGINT NOT NULL,
agent_principal_id BIGINT NOT NULL,
active_agent_id BIGINT NULL,
mode VARCHAR(16) NOT NULL,
state VARCHAR(16) NOT NULL,
step VARCHAR(16) NOT NULL,
reason VARCHAR(2000) NOT NULL,
agent_version INT NOT NULL,
version INT NOT NULL,
created_by BIGINT NOT NULL,
created_at TIMESTAMPTZ(3) NOT NULL,
step_started_at TIMESTAMPTZ(3) NOT NULL,
completed_at TIMESTAMPTZ(3) NULL,
CHECK (mode IN ('NORMAL','FORCED')),
CHECK (state IN ('IN_PROGRESS','COMPLETED','CANCELLED','FAILED')),
CHECK (step IN ('PRECHECK','HANDOVER','SUCCESSOR','REASSIGN','CLEANUP','REVOKE','DONE')),
CHECK (version > 0 AND agent_version > 0),
CHECK ((state = 'IN_PROGRESS' AND active_agent_id = agent_principal_id AND active_agent_id IS NOT NULL AND completed_at IS NULL)
 OR (state <> 'IN_PROGRESS' AND active_agent_id IS NULL AND completed_at IS NOT NULL))
);
CREATE UNIQUE INDEX {{.Prefix}}agents_retirements_active ON {{.Prefix}}agents_retirements (tenant_id, active_agent_id);
CREATE INDEX {{.Prefix}}agents_retirements_agent ON {{.Prefix}}agents_retirements (tenant_id, agent_principal_id, id);
-- +goose Down
DROP TABLE {{.Prefix}}agents_retirements;
