-- +goose Up
CREATE TABLE {{.Prefix}}fleet_agents (
 id BIGINT NOT NULL,
 principal_id BIGINT NOT NULL,
 name VARCHAR(64) NOT NULL,
 handle VARCHAR(40) NOT NULL,
 persona VARCHAR(2000) NULL,
 avatar_style VARCHAR(32) NOT NULL DEFAULT 'PIXEL',
 avatar_seed BIGINT NOT NULL,
 system_prompt TEXT NOT NULL,
 machine_id BIGINT NULL,
 runtime_kind VARCHAR(32) NOT NULL,
 model_id VARCHAR(160) NOT NULL,
 reasoning_effort VARCHAR(16) NULL,
 authorization_mode VARCHAR(32) NOT NULL DEFAULT 'ON_REQUEST',
 max_context_tokens INTEGER NOT NULL DEFAULT 24000,
 mcp_allowlist JSONB NOT NULL DEFAULT '[]',
 capability_tags JSONB NOT NULL DEFAULT '[]',
 default_channel_names JSONB NOT NULL DEFAULT '[]',
 memory_policy JSONB NOT NULL DEFAULT '{"profile":"STANDARD"}',
 max_parallel_items INTEGER NOT NULL DEFAULT 1,
 max_risk VARCHAR(32) NOT NULL DEFAULT 'MEDIUM',
 state VARCHAR(32) NOT NULL DEFAULT 'DRAFT',
 process_status VARCHAR(32) NOT NULL DEFAULT 'NONE',
 process_status_at TIMESTAMPTZ(3) NULL,
 last_seen_at TIMESTAMPTZ(3) NULL,
 config_revision INTEGER NOT NULL DEFAULT 1,
 running_config_revision INTEGER NULL,
 auto_restart BOOLEAN NOT NULL DEFAULT true,
 restart_window_started_at TIMESTAMPTZ(3) NULL,
 restart_count_in_window INTEGER NOT NULL DEFAULT 0,
 restart_exhausted BOOLEAN NOT NULL DEFAULT false,
 credential_id BIGINT NULL,
 credential_delivery VARCHAR(32) NOT NULL DEFAULT 'PENDING',
 pending_cleanup_machine_id BIGINT NULL,
 retirement_id BIGINT NULL,
 retired_at TIMESTAMPTZ(3) NULL,
 version INTEGER NOT NULL DEFAULT 1,
 origin VARCHAR(32) NOT NULL DEFAULT 'HUMAN_CREATED',
 created_by_principal_id BIGINT NOT NULL,
 parent_agent_id BIGINT NULL,
 owner_principal_id BIGINT NOT NULL,
 spawn_grant_id BIGINT NULL,
 spawn_request_id BIGINT NULL,
 spawn_reason VARCHAR(2000) NULL,
 expires_at TIMESTAMPTZ(3) NULL,
 idle_since TIMESTAMPTZ(3) NULL,
 adopted_by BIGINT NULL,
 adopted_at TIMESTAMPTZ(3) NULL,
 orphaned_at TIMESTAMPTZ(3) NULL,
 orphan_reason VARCHAR(32) NULL,
 model_family VARCHAR(32) NOT NULL,
 tenant_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 created_by BIGINT NULL,
 updated_by BIGINT NULL,
 PRIMARY KEY (id),
 CONSTRAINT {{.Prefix}}uq_fleet_agents_principal UNIQUE (principal_id),
 CONSTRAINT {{.Prefix}}uq_fleet_agents_handle UNIQUE (tenant_id,handle),
 CHECK (avatar_style IN ('ROBOT','PIXEL','MONSTER','GEOMETRIC')),
 CHECK (runtime_kind IN ('CLAUDE_CODE','CODEX')),
 CHECK (authorization_mode IN ('READ_ONLY','ON_REQUEST','FULL_AUTO')),
 CHECK (max_risk IN ('LOW','MEDIUM','HIGH')),
 CHECK (state IN ('DRAFT','ACTIVE','STOPPED','CRASHED','RETIRING','RETIRED')),
 CHECK (process_status IN ('NONE','STARTING','RUNNING','STOPPING','EXITED','UNREACHABLE')),
 CHECK (credential_delivery IN ('PENDING','DELIVERED','FAILED')),
 CHECK (origin IN ('HUMAN_CREATED','AGENT_SPAWNED')),
 CHECK (orphan_reason IN ('SUPERVISOR_RETIRED','SUPERVISOR_CRASHED','GRANT_REVOKED','GRANT_EXPIRED','TAKEOVER')),
 CHECK (avatar_seed>=0 AND avatar_seed<9007199254740992),
 CHECK (max_context_tokens BETWEEN 4000 AND 200000),
 CHECK (max_parallel_items BETWEEN 1 AND 4),
 CHECK (restart_count_in_window BETWEEN 0 AND 3),
 CHECK (origin='HUMAN_CREATED' OR (parent_agent_id IS NOT NULL AND spawn_grant_id IS NOT NULL AND spawn_reason IS NOT NULL))
);
CREATE TABLE {{.Prefix}}fleet_agent_roles (
 agent_id BIGINT NOT NULL,
 role VARCHAR(32) NOT NULL,
 iam_role_key VARCHAR(64) NOT NULL,
 revoked_at TIMESTAMPTZ(3) NULL,
 tenant_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 created_by BIGINT NULL,
 updated_by BIGINT NULL,
 PRIMARY KEY (agent_id,role),
 CHECK (role IN ('EXECUTOR','REVIEWER','COORDINATOR','LEAD','TESTER','ACCEPTOR','ASSISTANT')),
 CHECK ((role='EXECUTOR' AND iam_role_key='agent_executor') OR (role='REVIEWER' AND iam_role_key='agent_reviewer') OR (role='COORDINATOR' AND iam_role_key='agent_coordinator') OR (role='LEAD' AND iam_role_key='agent_lead') OR (role='TESTER' AND iam_role_key='agent_tester') OR (role='ACCEPTOR' AND iam_role_key='agent_acceptor') OR (role='ASSISTANT' AND iam_role_key='agent_assistant'))
);
CREATE UNIQUE INDEX {{.Prefix}}uq_fleet_agents_active_name ON {{.Prefix}}fleet_agents (tenant_id,LOWER(name)) WHERE state<>'RETIRED';
CREATE INDEX {{.Prefix}}idx_fleet_agents_state ON {{.Prefix}}fleet_agents (tenant_id,state);
CREATE INDEX {{.Prefix}}idx_fleet_agents_machine ON {{.Prefix}}fleet_agents (tenant_id,machine_id,state);
CREATE INDEX {{.Prefix}}idx_fleet_agents_watchdog ON {{.Prefix}}fleet_agents (state,process_status,last_seen_at);
CREATE INDEX {{.Prefix}}idx_fleet_agents_origin ON {{.Prefix}}fleet_agents (tenant_id,origin,state);
CREATE INDEX {{.Prefix}}idx_fleet_agents_parent ON {{.Prefix}}fleet_agents (tenant_id,parent_agent_id,state);
CREATE INDEX {{.Prefix}}idx_fleet_agents_grant ON {{.Prefix}}fleet_agents (tenant_id,spawn_grant_id,state);
CREATE INDEX {{.Prefix}}idx_fleet_agents_expiry ON {{.Prefix}}fleet_agents (expires_at);
CREATE INDEX {{.Prefix}}idx_fleet_agents_orphan ON {{.Prefix}}fleet_agents (tenant_id,orphaned_at);
CREATE INDEX {{.Prefix}}idx_fleet_agent_roles_tenant ON {{.Prefix}}fleet_agent_roles (tenant_id,agent_id,revoked_at);
-- +goose Down
DROP TABLE {{.Prefix}}fleet_agent_roles;
DROP TABLE {{.Prefix}}fleet_agents;
