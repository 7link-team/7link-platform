-- +goose Up
CREATE TABLE {{.Prefix}}planning_goals (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,principal_id BIGINT NOT NULL,
 period_kind VARCHAR(16) NOT NULL,period_start VARCHAR(10) NOT NULL,state VARCHAR(32) NOT NULL,
 content MEDIUMTEXT NOT NULL,content_hash VARCHAR(64) NOT NULL,version INTEGER NOT NULL,
 created_by BIGINT NOT NULL,created_at DATETIME(3) NOT NULL,updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id)
);
CREATE INDEX {{.Prefix}}planning_goals_scope ON {{.Prefix}}planning_goals(tenant_id,project_id,principal_id,id);
CREATE TABLE {{.Prefix}}planning_plans (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,principal_id BIGINT NOT NULL,
 period_kind VARCHAR(16) NOT NULL,period_start VARCHAR(10) NOT NULL,state VARCHAR(32) NOT NULL,
 content MEDIUMTEXT NOT NULL,content_hash VARCHAR(64) NOT NULL,version INTEGER NOT NULL,
 created_by BIGINT NOT NULL,created_at DATETIME(3) NOT NULL,updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,project_id,principal_id,period_kind,period_start)
);
CREATE INDEX {{.Prefix}}planning_plans_scope ON {{.Prefix}}planning_plans(tenant_id,project_id,principal_id,id);
CREATE TABLE {{.Prefix}}planning_reports (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,principal_id BIGINT NOT NULL,
 period_kind VARCHAR(16) NOT NULL,period_start VARCHAR(10) NOT NULL,state VARCHAR(32) NOT NULL,
 content MEDIUMTEXT NOT NULL,content_hash VARCHAR(64) NOT NULL,version INTEGER NOT NULL,
 created_by BIGINT NOT NULL,created_at DATETIME(3) NOT NULL,updated_at DATETIME(3) NOT NULL,facts MEDIUMTEXT NOT NULL,confirmed_by BIGINT NULL,confirmed_at DATETIME(3) NULL,
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,project_id,principal_id,period_kind,period_start)
);
CREATE INDEX {{.Prefix}}planning_reports_scope ON {{.Prefix}}planning_reports(tenant_id,project_id,principal_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}planning_reports;
DROP TABLE {{.Prefix}}planning_plans;
DROP TABLE {{.Prefix}}planning_goals;
