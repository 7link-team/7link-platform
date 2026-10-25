-- +goose Up
CREATE TABLE {{.Prefix}}delivery_assignments (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 role VARCHAR(16) NOT NULL,
 assignee_kind VARCHAR(16) NOT NULL,
 assignee_principal_id BIGINT NOT NULL,
 agent_id BIGINT NULL,
 status VARCHAR(16) NOT NULL,
 active_slot BIGINT NULL,
 lease_ttl_ms BIGINT NOT NULL,
 lease_expires_at TIMESTAMPTZ(3) NULL,
 lease_renewed_at TIMESTAMPTZ(3) NULL,
 decision_id BIGINT NULL,
 ended_at TIMESTAMPTZ(3) NULL,
 end_reason VARCHAR(64) NULL,
 ensemble_plan_id BIGINT NULL,
 version INT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 created_by BIGINT NULL,
 updated_by BIGINT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_assignment_active UNIQUE (tenant_id,active_slot,role),
 CONSTRAINT {{.Prefix}}fk_delivery_assignment_work FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (version>0 AND lease_ttl_ms>0),
 CHECK (role IN ('EXECUTOR','REVIEWER')),
 CHECK (status IN ('ACTIVE','RELEASED','EXPIRED','REVOKED','COMPLETED')),
 CHECK ((assignee_kind='HUMAN' AND agent_id IS NULL) OR (assignee_kind='AGENT' AND agent_id IS NOT NULL)),
 CHECK ((status='ACTIVE' AND ensemble_plan_id IS NULL AND active_slot IS NOT NULL AND active_slot=work_item_id) OR ((status<>'ACTIVE' OR ensemble_plan_id IS NOT NULL) AND active_slot IS NULL)),
 CHECK ((status='ACTIVE' AND ended_at IS NULL AND end_reason IS NULL) OR (status<>'ACTIVE' AND ended_at IS NOT NULL AND end_reason IS NOT NULL))
);
CREATE INDEX {{.Prefix}}idx_delivery_assignment_lease ON {{.Prefix}}delivery_assignments (status,lease_expires_at,tenant_id,id);
CREATE INDEX {{.Prefix}}idx_delivery_assignment_holder ON {{.Prefix}}delivery_assignments (tenant_id,assignee_principal_id,status);
CREATE INDEX {{.Prefix}}idx_delivery_assignment_work ON {{.Prefix}}delivery_assignments (tenant_id,project_id,work_item_id,created_at,id);

-- +goose Down
-- Ownership and lease history must not be erased by a rollback.
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}delivery_assignment_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_assignment_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_assignments;
DROP TABLE {{.Prefix}}delivery_assignment_rollback_guard;
DROP TABLE {{.Prefix}}delivery_assignments;
