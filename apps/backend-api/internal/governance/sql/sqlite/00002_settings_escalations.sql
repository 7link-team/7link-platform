-- +goose Up
CREATE TABLE {{.Prefix}}governance_settings (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 value_json TEXT NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_settings_tenant UNIQUE(tenant_id)
);
CREATE TABLE {{.Prefix}}governance_escalation_rules (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 kind VARCHAR(48) NOT NULL,
 value_json TEXT NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_escalation_rules_kind UNIQUE(tenant_id,kind)
);
CREATE TABLE {{.Prefix}}governance_escalation_incidents (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 kind VARCHAR(48) NOT NULL,
 subject_kind VARCHAR(32) NOT NULL,
 subject_id INTEGER NOT NULL,
 trigger_event_id INTEGER NOT NULL,
 reason VARCHAR(2000) NOT NULL,
 facts_json TEXT NOT NULL,
 state VARCHAR(32) NOT NULL CHECK(state IN ('OPEN','ACKNOWLEDGED','RESOLVED','DISMISSED')),
 open_key VARCHAR(160) NULL,
 version INT NOT NULL CHECK(version > 0),
 handled_by INTEGER NULL,
 handled_at DATETIME NULL,
 resolution_note VARCHAR(2000) NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_incident_event UNIQUE(tenant_id,trigger_event_id,kind,subject_id),
 CONSTRAINT {{.Prefix}}governance_incident_open UNIQUE(tenant_id,open_key)
);
CREATE INDEX {{.Prefix}}governance_incident_project ON {{.Prefix}}governance_escalation_incidents(tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_escalation_incidents;
DROP TABLE {{.Prefix}}governance_escalation_rules;
DROP TABLE {{.Prefix}}governance_settings;
