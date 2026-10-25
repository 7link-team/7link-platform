-- +goose Up
CREATE TABLE {{.Prefix}}delivery_iterations (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, name VARCHAR(120) NOT NULL, version INTEGER NOT NULL, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL, goal VARCHAR(500) NOT NULL, goal_id BIGINT NULL, starts_at TIMESTAMPTZ(3) NOT NULL, ends_at TIMESTAMPTZ(3) NOT NULL, state VARCHAR(16) NOT NULL, closed_at TIMESTAMPTZ(3) NULL,
 CONSTRAINT {{.Prefix}}uq_iteration_name UNIQUE(tenant_id,project_id,name),
 CONSTRAINT {{.Prefix}}ck_iteration_window CHECK(ends_at > starts_at),
 CONSTRAINT {{.Prefix}}ck_iteration_state CHECK(state IN ('PLANNED','ACTIVE','CLOSED'))
);
CREATE INDEX {{.Prefix}}idx_iteration_state ON {{.Prefix}}delivery_iterations(tenant_id,project_id,state,starts_at);
CREATE TABLE {{.Prefix}}delivery_versions (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, name VARCHAR(120) NOT NULL, version INTEGER NOT NULL, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL, planned_release_at TIMESTAMPTZ(3) NULL, released_at TIMESTAMPTZ(3) NULL, state VARCHAR(16) NOT NULL, locked_at TIMESTAMPTZ(3) NULL, locked_by BIGINT NULL, delivery_id BIGINT NULL,
 CONSTRAINT {{.Prefix}}uq_version_name UNIQUE(tenant_id,project_id,name),
 CONSTRAINT {{.Prefix}}ck_version_state CHECK(state IN ('PLANNING','LOCKED','RELEASED','ARCHIVED'))
);
CREATE INDEX {{.Prefix}}idx_version_project ON {{.Prefix}}delivery_versions(tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}delivery_milestones (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, name VARCHAR(120) NOT NULL, version INTEGER NOT NULL, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL, due_at TIMESTAMPTZ(3) NOT NULL, achieved_at TIMESTAMPTZ(3) NULL, goal_id BIGINT NULL
);
CREATE INDEX {{.Prefix}}idx_milestone_project ON {{.Prefix}}delivery_milestones(tenant_id,project_id,due_at);
CREATE TABLE {{.Prefix}}delivery_work_planning (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL, iteration_id BIGINT NULL, version_id BIGINT NULL, milestone_id BIGINT NULL, carried_from_id BIGINT NULL, carried_at TIMESTAMPTZ(3) NULL, version INTEGER NOT NULL, updated_by BIGINT NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_work_planning UNIQUE(tenant_id,project_id,work_item_id),
 CONSTRAINT {{.Prefix}}fk_planning_iteration FOREIGN KEY(iteration_id) REFERENCES {{.Prefix}}delivery_iterations(id),
 CONSTRAINT {{.Prefix}}fk_planning_version FOREIGN KEY(version_id) REFERENCES {{.Prefix}}delivery_versions(id),
 CONSTRAINT {{.Prefix}}fk_planning_milestone FOREIGN KEY(milestone_id) REFERENCES {{.Prefix}}delivery_milestones(id)
);
CREATE INDEX {{.Prefix}}idx_work_planning_iteration ON {{.Prefix}}delivery_work_planning(tenant_id,project_id,iteration_id);
CREATE TABLE {{.Prefix}}delivery_planning_carryovers (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, from_iteration_id BIGINT NOT NULL, to_iteration_id BIGINT NULL, carried_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_planning_carryover UNIQUE(tenant_id,from_iteration_id,work_item_id)
);
CREATE INDEX {{.Prefix}}idx_planning_carryover_period ON {{.Prefix}}delivery_planning_carryovers(tenant_id,project_id,carried_at);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_planning_carryovers;
DROP TABLE {{.Prefix}}delivery_work_planning;
DROP TABLE {{.Prefix}}delivery_milestones;
DROP TABLE {{.Prefix}}delivery_versions;
DROP TABLE {{.Prefix}}delivery_iterations;
