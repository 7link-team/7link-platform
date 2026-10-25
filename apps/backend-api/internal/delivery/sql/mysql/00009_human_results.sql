-- +goose Up
-- Immutable Human result claims, exact-version evidence and handoff history.
CREATE TABLE {{.Prefix}}delivery_result_versions (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 sequence INT NOT NULL,
 summary VARCHAR(2000) NOT NULL,
 actor_id BIGINT NOT NULL,
 baseline_version_no INT NOT NULL,
 previous_result_id BIGINT NULL,
 rework_action_id BIGINT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_result_versions_ref UNIQUE (tenant_id,project_id,work_item_id,id),
 CONSTRAINT {{.Prefix}}uq_result_sequence UNIQUE (tenant_id,work_item_id,sequence),
 CHECK (sequence>0 AND length(trim(summary))>0),
 CONSTRAINT {{.Prefix}}fk_result_versions_work FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_result_versions_work ON {{.Prefix}}delivery_result_versions (tenant_id,project_id,work_item_id,id);
CREATE TABLE {{.Prefix}}delivery_result_evidence (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 result_id BIGINT NOT NULL,
 binding_id BIGINT NOT NULL,
 space_id BIGINT NOT NULL,
 file_id BIGINT NOT NULL,
 version_id BIGINT NOT NULL,
 criterion_id BIGINT NULL,
 coverage VARCHAR(24) NOT NULL,
 note VARCHAR(2000) NOT NULL,
 name VARCHAR(255) NOT NULL,
 sha256 CHAR(64) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_result_evidence_ref UNIQUE (tenant_id,project_id,work_item_id,id),
 CHECK (coverage IN ('COVERED','PARTIAL','NOT_APPLICABLE')),
 CHECK (coverage<>'NOT_APPLICABLE' OR length(trim(note))>0),
 CHECK (length(sha256)=64),
 CONSTRAINT {{.Prefix}}fk_result_evidence_work FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_result_evidence_result FOREIGN KEY (tenant_id,project_id,work_item_id,result_id) REFERENCES {{.Prefix}}delivery_result_versions (tenant_id,project_id,work_item_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_result_evidence_work ON {{.Prefix}}delivery_result_evidence (tenant_id,project_id,work_item_id,id);
CREATE TABLE {{.Prefix}}delivery_result_handoffs (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 result_id BIGINT NOT NULL,
 delivery_id BIGINT NOT NULL,
 summary VARCHAR(2000) NOT NULL,
 manifest TEXT NOT NULL,
 sha256 CHAR(64) NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_result_handoffs_ref UNIQUE (tenant_id,project_id,work_item_id,id),
 CONSTRAINT {{.Prefix}}fk_handoff_delivery FOREIGN KEY (tenant_id,project_id,delivery_id) REFERENCES {{.Prefix}}delivery_deliveries (tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (length(sha256)=64 AND length(trim(summary))>0),
 CONSTRAINT {{.Prefix}}fk_result_handoffs_work FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_result_handoffs_result FOREIGN KEY (tenant_id,project_id,work_item_id,result_id) REFERENCES {{.Prefix}}delivery_result_versions (tenant_id,project_id,work_item_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_result_handoffs_work ON {{.Prefix}}delivery_result_handoffs (tenant_id,project_id,work_item_id,id);
CREATE TABLE {{.Prefix}}delivery_result_actions (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 result_id BIGINT NOT NULL,
 kind VARCHAR(16) NOT NULL,
 verdict VARCHAR(16) NOT NULL,
 actor_id BIGINT NOT NULL,
 authority_source VARCHAR(96) NOT NULL,
 reason VARCHAR(2000) NOT NULL,
 handoff_id BIGINT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_result_actions_ref UNIQUE (tenant_id,project_id,work_item_id,id),
 CONSTRAINT {{.Prefix}}fk_action_handoff FOREIGN KEY (tenant_id,project_id,work_item_id,handoff_id) REFERENCES {{.Prefix}}delivery_result_handoffs (tenant_id,project_id,work_item_id,id) ON DELETE RESTRICT,
 CHECK (kind IN ('CHECK','ACCEPT','RETURN','RECEIPT')),
 CHECK (verdict IN ('PASSED','FAILED','INCONCLUSIVE')),
 CHECK (length(trim(reason))>0 AND length(trim(authority_source))>0),
 CONSTRAINT {{.Prefix}}fk_result_actions_work FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_result_actions_result FOREIGN KEY (tenant_id,project_id,work_item_id,result_id) REFERENCES {{.Prefix}}delivery_result_versions (tenant_id,project_id,work_item_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_result_actions_work ON {{.Prefix}}delivery_result_actions (tenant_id,project_id,work_item_id,id);

-- +goose Down
-- Refuse destructive rollback when immutable result history exists.
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_result_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_result_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_result_versions;
INSERT INTO {{.Prefix}}delivery_result_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_result_evidence;
INSERT INTO {{.Prefix}}delivery_result_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_result_handoffs;
INSERT INTO {{.Prefix}}delivery_result_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_result_actions;
DROP TABLE {{.Prefix}}delivery_result_rollback_guard;
DROP TABLE {{.Prefix}}delivery_result_actions;
DROP TABLE {{.Prefix}}delivery_result_handoffs;
DROP TABLE {{.Prefix}}delivery_result_evidence;
DROP TABLE {{.Prefix}}delivery_result_versions;
