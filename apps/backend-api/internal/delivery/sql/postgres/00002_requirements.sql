-- +goose Up
CREATE TABLE {{.Prefix}}delivery_project_counters (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 next_requirement_no INT NOT NULL DEFAULT 1,
 next_work_item_no INT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 created_by BIGINT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_counter_project UNIQUE (tenant_id,project_id),
 CONSTRAINT {{.Prefix}}fk_delivery_counter_project FOREIGN KEY (tenant_id,project_id) REFERENCES {{.Prefix}}delivery_projects (tenant_id,project_id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}chk_delivery_counter_next CHECK (next_requirement_no>0 AND next_work_item_no>0)
 );
CREATE TABLE {{.Prefix}}delivery_requirements (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 number INT NOT NULL,
 title VARCHAR(200) NOT NULL,
 description TEXT NOT NULL DEFAULT (''),
 priority SMALLINT NOT NULL DEFAULT 2,
 status VARCHAR(32) COLLATE "C" NOT NULL DEFAULT 'DRAFT',
 current_version_no INT NOT NULL DEFAULT 0,
 source_intake_id BIGINT NULL,
 archived_at TIMESTAMPTZ(3) NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 created_by BIGINT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 updated_by BIGINT NULL,
 version INT NOT NULL DEFAULT 1,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_req_number UNIQUE (tenant_id,project_id,number),
 CONSTRAINT {{.Prefix}}uq_delivery_req_project_id UNIQUE (tenant_id,project_id,id),
 CONSTRAINT {{.Prefix}}fk_delivery_req_project FOREIGN KEY (tenant_id,project_id) REFERENCES {{.Prefix}}delivery_projects (tenant_id,project_id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}chk_delivery_req_number CHECK (number>0),
 CONSTRAINT {{.Prefix}}chk_delivery_req_title CHECK (length(trim(title))>0),
 CONSTRAINT {{.Prefix}}chk_delivery_req_priority CHECK (priority BETWEEN 0 AND 3),
 CONSTRAINT {{.Prefix}}chk_delivery_req_status CHECK (status IN ('DRAFT','BASELINED','ARCHIVED')),
 CONSTRAINT {{.Prefix}}chk_delivery_req_version CHECK (version>0 AND current_version_no BETWEEN 0 AND 200 AND (status<>'BASELINED' OR current_version_no>0))
 );
CREATE TABLE {{.Prefix}}delivery_acceptance_criteria (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 requirement_id BIGINT NOT NULL,
 code VARCHAR(16) NOT NULL,
 text VARCHAR(2000) NOT NULL,
 required BOOLEAN NOT NULL DEFAULT true,
 position SMALLINT NOT NULL,
 introduced_in_version INT NOT NULL DEFAULT 0,
 retired_in_version INT NULL,
 supersedes_id BIGINT NULL,
 archived_at TIMESTAMPTZ(3) NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 created_by BIGINT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_criterion_code UNIQUE (tenant_id,requirement_id,code),
 CONSTRAINT {{.Prefix}}uq_delivery_criterion_ref UNIQUE (tenant_id,project_id,requirement_id,id),
 CONSTRAINT {{.Prefix}}fk_delivery_criterion_req FOREIGN KEY (tenant_id,project_id,requirement_id) REFERENCES {{.Prefix}}delivery_requirements (tenant_id,project_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_delivery_criterion_prior FOREIGN KEY (tenant_id,project_id,requirement_id,supersedes_id) REFERENCES {{.Prefix}}delivery_acceptance_criteria (tenant_id,project_id,requirement_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}chk_delivery_criterion_position CHECK (position BETWEEN 0 AND 49),
 CONSTRAINT {{.Prefix}}chk_delivery_criterion_version CHECK (introduced_in_version BETWEEN 0 AND 200 AND (retired_in_version IS NULL OR retired_in_version>introduced_in_version AND retired_in_version<=200))
 );
CREATE TABLE {{.Prefix}}delivery_requirement_versions (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 requirement_id BIGINT NOT NULL,
 version_no INT NOT NULL,
 title VARCHAR(200) NOT NULL,
 description TEXT NOT NULL,
 priority SMALLINT NOT NULL,
 criteria JSONB NOT NULL,
 change_record_id BIGINT NULL,
 content_hash CHAR(64) NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 created_by BIGINT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_req_version UNIQUE (tenant_id,requirement_id,version_no),
 CONSTRAINT {{.Prefix}}fk_delivery_req_version_req FOREIGN KEY (tenant_id,project_id,requirement_id) REFERENCES {{.Prefix}}delivery_requirements (tenant_id,project_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}chk_delivery_req_snapshot_version CHECK (version_no BETWEEN 1 AND 200),
 CONSTRAINT {{.Prefix}}chk_delivery_req_snapshot_priority CHECK (priority BETWEEN 0 AND 3),
 CONSTRAINT {{.Prefix}}chk_delivery_req_snapshot_criteria CHECK (jsonb_typeof(criteria)='array'),
 CONSTRAINT {{.Prefix}}chk_delivery_req_snapshot_hash CHECK (length(content_hash)=64),
 CONSTRAINT {{.Prefix}}chk_delivery_req_snapshot_change CHECK ((version_no=1 AND change_record_id IS NULL) OR (version_no>1 AND change_record_id IS NOT NULL))
 );
CREATE INDEX {{.Prefix}}idx_delivery_req_list ON {{.Prefix}}delivery_requirements (tenant_id,project_id,status,priority,number);
CREATE INDEX {{.Prefix}}idx_delivery_req_intake ON {{.Prefix}}delivery_requirements (tenant_id,source_intake_id);
CREATE INDEX {{.Prefix}}idx_delivery_criterion_list ON {{.Prefix}}delivery_acceptance_criteria (tenant_id,requirement_id,retired_in_version,position);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_requirement_versions;
DROP TABLE {{.Prefix}}delivery_acceptance_criteria;
DROP TABLE {{.Prefix}}delivery_requirements;
DROP TABLE {{.Prefix}}delivery_project_counters;
