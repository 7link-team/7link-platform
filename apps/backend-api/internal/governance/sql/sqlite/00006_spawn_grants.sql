-- +goose Up
CREATE TABLE {{.Prefix}}governance_spawn_grants (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 grantee_agent_id INTEGER NOT NULL,
 grantee_principal_id INTEGER NOT NULL,
 granted_by INTEGER NOT NULL,
 scope_json TEXT NOT NULL,
 reason VARCHAR(2000) NOT NULL,
 starts_at DATETIME NOT NULL,
 ends_at DATETIME NOT NULL,
 state VARCHAR(16) NOT NULL CHECK(state IN ('ACTIVE','EXHAUSTED','REVOKED','EXPIRED')),
 active_key VARCHAR(32),
 end_reason VARCHAR(32),
 ended_at DATETIME,
 ended_by INTEGER,
 end_note VARCHAR(2000),
 active_count INT NOT NULL CHECK(active_count >= 0 AND active_count <= 10),
 spawned_day VARCHAR(10) NOT NULL,
 spawned_today INT NOT NULL CHECK(spawned_today >= 0 AND spawned_today <= 20),
 spent_micros BIGINT NOT NULL CHECK(spent_micros >= 0),
 replaces_grant_id INTEGER,
 version INT NOT NULL CHECK(version > 0),
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_spawn_grant_active UNIQUE(tenant_id,active_key),
 CONSTRAINT {{.Prefix}}governance_spawn_grant_window CHECK(ends_at > starts_at),
 CONSTRAINT {{.Prefix}}governance_spawn_grant_terminal CHECK((state IN ('ACTIVE','EXHAUSTED') AND active_key IS NOT NULL AND ended_at IS NULL AND end_reason IS NULL) OR (state IN ('REVOKED','EXPIRED') AND active_key IS NULL AND ended_at IS NOT NULL AND end_reason IS NOT NULL))
);
CREATE INDEX {{.Prefix}}governance_spawn_grant_expiry ON {{.Prefix}}governance_spawn_grants(tenant_id,state,ends_at);
CREATE TABLE {{.Prefix}}governance_spawn_grant_members (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 grant_id INTEGER NOT NULL,
 agent_id INTEGER NOT NULL,
 state VARCHAR(16) NOT NULL CHECK(state IN ('ACTIVE','RELEASED')),
 release_reason VARCHAR(32),
 per_agent_cap_micros BIGINT NOT NULL CHECK(per_agent_cap_micros > 0),
 spent_micros BIGINT NOT NULL CHECK(spent_micros >= 0),
 cap_notified BOOLEAN NOT NULL,
 reserved_at DATETIME NOT NULL,
 released_at DATETIME,
 CONSTRAINT {{.Prefix}}governance_spawn_member_agent UNIQUE(tenant_id,agent_id),
 CONSTRAINT {{.Prefix}}governance_spawn_member_terminal CHECK((state='ACTIVE' AND released_at IS NULL AND release_reason IS NULL) OR (state='RELEASED' AND released_at IS NOT NULL AND release_reason IS NOT NULL))
);
CREATE INDEX {{.Prefix}}governance_spawn_member_grant ON {{.Prefix}}governance_spawn_grant_members(tenant_id,grant_id,state);
CREATE TABLE {{.Prefix}}governance_spawn_cost_cursors (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 execution_id INTEGER NOT NULL,
 agent_id INTEGER NOT NULL,
 last_cumulative_micros BIGINT NOT NULL CHECK(last_cumulative_micros >= 0),
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_spawn_cost_execution UNIQUE(tenant_id,execution_id)
);
-- +goose Down
DROP TABLE {{.Prefix}}governance_spawn_cost_cursors;
DROP TABLE {{.Prefix}}governance_spawn_grant_members;
DROP TABLE {{.Prefix}}governance_spawn_grants;
