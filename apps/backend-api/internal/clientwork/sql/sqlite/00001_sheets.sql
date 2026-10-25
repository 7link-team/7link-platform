-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_sheets (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 title VARCHAR(200) NOT NULL,
 current_revision INTEGER NOT NULL,
 published_revision INTEGER NOT NULL,
 state VARCHAR(32) NOT NULL,
 version INTEGER NOT NULL,
 created_by BIGINT NOT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_sheets_project ON {{.Prefix}}clientwork_sheets (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_sheet_revisions (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 sheet_id BIGINT NOT NULL,
 revision INTEGER NOT NULL,
 content TEXT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 signer_id BIGINT NOT NULL,
 created_by BIGINT NOT NULL,
 created_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id,id),
 UNIQUE(tenant_id,sheet_id,revision)
);
CREATE INDEX {{.Prefix}}clientwork_sheet_revisions_project ON {{.Prefix}}clientwork_sheet_revisions (tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_signoffs (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 sheet_id BIGINT NOT NULL,
 revision_id BIGINT NOT NULL,
 decision VARCHAR(32) NOT NULL,
 reason TEXT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id,id),
 UNIQUE(tenant_id,revision_id)
);
CREATE INDEX {{.Prefix}}clientwork_signoffs_project ON {{.Prefix}}clientwork_signoffs (tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_signoffs;
DROP TABLE {{.Prefix}}clientwork_sheet_revisions;
DROP TABLE {{.Prefix}}clientwork_sheets;
