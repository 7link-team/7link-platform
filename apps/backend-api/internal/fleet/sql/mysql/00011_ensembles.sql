-- +goose Up
CREATE TABLE {{.Prefix}}fleet_ensemble_plans (
 id BIGINT PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL,
 state VARCHAR(32) NOT NULL CHECK (state IN ('DRAFT','COMPARING','SELECTED','SYNTHESIZED','ABANDONED')),
 reason TEXT NOT NULL, comparison_json TEXT NOT NULL, selected_branch_id BIGINT NULL, synthesis_execution_id BIGINT NULL,
 created_by BIGINT NOT NULL, version INT NOT NULL CHECK(version > 0), created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL,
 UNIQUE(tenant_id,project_id,id)
);
CREATE TABLE {{.Prefix}}fleet_ensemble_branches (
 id BIGINT PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, plan_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL, archived_at DATETIME(3) NULL,
 UNIQUE(tenant_id,plan_id,execution_id),
 CONSTRAINT {{.Prefix}}fk_ensemble_branch_plan FOREIGN KEY(tenant_id,project_id,plan_id) REFERENCES {{.Prefix}}fleet_ensemble_plans(tenant_id,project_id,id)
);
-- +goose Down
DROP TABLE {{.Prefix}}fleet_ensemble_branches;
DROP TABLE {{.Prefix}}fleet_ensemble_plans;
