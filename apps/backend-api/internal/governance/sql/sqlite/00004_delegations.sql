-- +goose Up
CREATE TABLE {{.Prefix}}governance_delegations (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 grantee_principal_id INTEGER NOT NULL,
 grantee_agent_id INTEGER NOT NULL,
 grantor_principal_id INTEGER NOT NULL,
 conditions_json TEXT NOT NULL,
 iam_bindings TEXT NOT NULL,
 starts_at DATETIME NOT NULL,
 ends_at DATETIME NOT NULL,
 reason VARCHAR(2000) NOT NULL,
 state VARCHAR(16) NOT NULL CHECK(state IN ('ACTIVE','REVOKED','EXPIRED')),
 end_reason VARCHAR(32),
 ended_at DATETIME,
 ended_by INTEGER,
 end_note VARCHAR(2000),
 active_key VARCHAR(48),
 replaces_delegation_id INTEGER,
 version INTEGER NOT NULL CHECK(version > 0),
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_delegation_active UNIQUE(tenant_id,active_key),
 CONSTRAINT {{.Prefix}}governance_delegation_window CHECK(ends_at > starts_at),
 CONSTRAINT {{.Prefix}}governance_delegation_terminal CHECK((state='ACTIVE' AND active_key IS NOT NULL AND ended_at IS NULL AND end_reason IS NULL) OR (state<>'ACTIVE' AND active_key IS NULL AND ended_at IS NOT NULL AND end_reason IS NOT NULL))
);
CREATE INDEX {{.Prefix}}governance_delegation_expiry ON {{.Prefix}}governance_delegations(tenant_id,state,ends_at);
CREATE INDEX {{.Prefix}}governance_delegation_project ON {{.Prefix}}governance_delegations(tenant_id,project_id,state);
CREATE INDEX {{.Prefix}}governance_delegation_grantee ON {{.Prefix}}governance_delegations(tenant_id,grantee_principal_id,state);
-- +goose Down
DROP TABLE {{.Prefix}}governance_delegations;
