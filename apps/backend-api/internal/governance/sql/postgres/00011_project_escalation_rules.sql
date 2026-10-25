-- +goose Up
CREATE TABLE {{.Prefix}}governance_project_escalation_rules (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL CHECK(project_id > 0),
 kind VARCHAR(48) NOT NULL,
 value_json JSONB NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}governance_project_rule_kind UNIQUE(tenant_id,project_id,kind)
);
-- +goose Down
DROP TABLE {{.Prefix}}governance_project_escalation_rules;
