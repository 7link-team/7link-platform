-- +goose Up
CREATE TABLE {{.Prefix}}delivery_code_links (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 target_kind VARCHAR(32) NOT NULL,
 target_id BIGINT NOT NULL,
 kind VARCHAR(32) NOT NULL,
 repository_id BIGINT NOT NULL,
 repository VARCHAR(300) NOT NULL,
 ref VARCHAR(200) NOT NULL,
 title VARCHAR(300) NOT NULL,
 url VARCHAR(500) NOT NULL,
 source VARCHAR(16) NOT NULL,
 provenance VARCHAR(32) NOT NULL,
 snapshot TEXT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 created_by BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_code_link_ref UNIQUE (tenant_id,target_kind,target_id,kind,repository_id,ref)
);
CREATE INDEX {{.Prefix}}idx_code_link_work ON {{.Prefix}}delivery_code_links (tenant_id,project_id,target_kind,target_id,id);
CREATE TABLE {{.Prefix}}delivery_code_link_scans (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 repository_id BIGINT NOT NULL,
 ref VARCHAR(200) NOT NULL,
 token VARCHAR(40) NOT NULL,
 reason VARCHAR(32) NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL
);
CREATE INDEX {{.Prefix}}idx_code_link_scan ON {{.Prefix}}delivery_code_link_scans (tenant_id,project_id,repository_id,id);

-- +goose Down
DROP TABLE {{.Prefix}}delivery_code_link_scans;
DROP TABLE {{.Prefix}}delivery_code_links;
