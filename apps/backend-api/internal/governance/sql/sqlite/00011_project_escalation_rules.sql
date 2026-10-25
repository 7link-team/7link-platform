-- +goose Up
CREATE TABLE {{.Prefix}}governance_project_escalation_rules (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL CHECK(project_id > 0),
 kind VARCHAR(48) NOT NULL,
 value_json TEXT NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_project_rule_kind UNIQUE(tenant_id,project_id,kind)
);
-- +goose Down
DROP TABLE {{.Prefix}}governance_project_escalation_rules;
