-- +goose Up
CREATE TABLE {{.Prefix}}governance_settings (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 value_json JSONB NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}governance_settings_tenant UNIQUE(tenant_id)
);
CREATE TABLE {{.Prefix}}governance_escalation_rules (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 kind VARCHAR(48) NOT NULL,
 value_json JSONB NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}governance_escalation_rules_kind UNIQUE(tenant_id,kind)
);
CREATE TABLE {{.Prefix}}governance_escalation_incidents (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 kind VARCHAR(48) NOT NULL,
 subject_kind VARCHAR(32) NOT NULL,
 subject_id BIGINT NOT NULL,
 trigger_event_id BIGINT NOT NULL,
 reason VARCHAR(2000) NOT NULL,
 facts_json JSONB NOT NULL,
 state VARCHAR(32) NOT NULL CHECK(state IN ('OPEN','ACKNOWLEDGED','RESOLVED','DISMISSED')),
 open_key VARCHAR(160) NULL,
 version INT NOT NULL CHECK(version > 0),
 handled_by BIGINT NULL,
 handled_at TIMESTAMPTZ(3) NULL,
 resolution_note VARCHAR(2000) NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}governance_incident_event UNIQUE(tenant_id,trigger_event_id,kind,subject_id),
 CONSTRAINT {{.Prefix}}governance_incident_open UNIQUE(tenant_id,open_key)
);
CREATE INDEX {{.Prefix}}governance_incident_project ON {{.Prefix}}governance_escalation_incidents(tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_escalation_incidents;
DROP TABLE {{.Prefix}}governance_escalation_rules;
DROP TABLE {{.Prefix}}governance_settings;
