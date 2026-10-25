-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_change_requests (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 sheet_id BIGINT NOT NULL,
 base_revision INTEGER NOT NULL,
 description LONGTEXT NOT NULL,
 state VARCHAR(32) NOT NULL,
 impact LONGTEXT NOT NULL,
 proposal_hash VARCHAR(64) NOT NULL,
 applied_change_id BIGINT NULL,
 actor_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_change_requests_project ON {{.Prefix}}clientwork_change_requests (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_issues (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 title VARCHAR(200) NOT NULL,
 description LONGTEXT NOT NULL,
 state VARCHAR(32) NOT NULL,
 artifact_id BIGINT NULL,
 anchor LONGTEXT NOT NULL,
 internal_defect_id BIGINT NULL,
 actor_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_issues_project ON {{.Prefix}}clientwork_issues (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_issue_replies (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 issue_id BIGINT NOT NULL,
 body LONGTEXT NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_issue_replies_project ON {{.Prefix}}clientwork_issue_replies (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_packages (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 title VARCHAR(200) NOT NULL,
 release_notes LONGTEXT NOT NULL,
 manifest LONGTEXT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 state VARCHAR(32) NOT NULL,
 recipient_id BIGINT NOT NULL,
 actor_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_packages_project ON {{.Prefix}}clientwork_packages (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_package_receipts (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 package_id BIGINT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 decision VARCHAR(32) NOT NULL,
 reason LONGTEXT NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id),
 UNIQUE(tenant_id,package_id)
);
CREATE INDEX {{.Prefix}}clientwork_package_receipts_project ON {{.Prefix}}clientwork_package_receipts (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_previews (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 title VARCHAR(200) NOT NULL,
 work_item_id BIGINT NOT NULL,
 result_id BIGINT NOT NULL,
 evidence_id BIGINT NOT NULL,
 file_version_id BIGINT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 client_visible BOOLEAN NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_previews_project ON {{.Prefix}}clientwork_previews (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_annotations (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 preview_id BIGINT NOT NULL,
 file_version_id BIGINT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 anchor LONGTEXT NOT NULL,
 body LONGTEXT NOT NULL,
 client_visible BOOLEAN NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_annotations_project ON {{.Prefix}}clientwork_annotations (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_test_cases (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 title VARCHAR(200) NOT NULL,
 steps LONGTEXT NOT NULL,
 expected LONGTEXT NOT NULL,
 work_item_id BIGINT NOT NULL,
 defect_id BIGINT NULL,
 version INTEGER NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_test_cases_project ON {{.Prefix}}clientwork_test_cases (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_test_plans (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 title VARCHAR(200) NOT NULL,
 cases LONGTEXT NOT NULL,
 state VARCHAR(32) NOT NULL,
 version INTEGER NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_test_plans_project ON {{.Prefix}}clientwork_test_plans (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_test_runs (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 plan_id BIGINT NOT NULL,
 case_id BIGINT NOT NULL,
 case_version INTEGER NOT NULL,
 result VARCHAR(32) NOT NULL,
 evidence LONGTEXT NOT NULL,
 defect_id BIGINT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_test_runs_project ON {{.Prefix}}clientwork_test_runs (tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_test_runs;
DROP TABLE {{.Prefix}}clientwork_test_plans;
DROP TABLE {{.Prefix}}clientwork_test_cases;
DROP TABLE {{.Prefix}}clientwork_annotations;
DROP TABLE {{.Prefix}}clientwork_previews;
DROP TABLE {{.Prefix}}clientwork_package_receipts;
DROP TABLE {{.Prefix}}clientwork_packages;
DROP TABLE {{.Prefix}}clientwork_issue_replies;
DROP TABLE {{.Prefix}}clientwork_issues;
DROP TABLE {{.Prefix}}clientwork_change_requests;
