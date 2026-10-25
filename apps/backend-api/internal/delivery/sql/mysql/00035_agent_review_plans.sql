-- +goose Up
ALTER TABLE {{.Prefix}}delivery_plans DROP CHECK {{.Prefix}}delivery_plan_source;
ALTER TABLE {{.Prefix}}delivery_plans ADD CONSTRAINT {{.Prefix}}delivery_plan_source CHECK(source_kind IN ('HUMAN','AGENT'));
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_agent_plan_rollback_guard(n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}delivery_agent_plan_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_plans WHERE source_kind='AGENT';
DROP TABLE {{.Prefix}}delivery_agent_plan_rollback_guard;
ALTER TABLE {{.Prefix}}delivery_plans DROP CHECK {{.Prefix}}delivery_plan_source;
ALTER TABLE {{.Prefix}}delivery_plans ADD CONSTRAINT {{.Prefix}}delivery_plan_source CHECK(source_kind='HUMAN');
