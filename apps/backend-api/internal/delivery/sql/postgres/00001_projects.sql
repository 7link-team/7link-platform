-- +goose Up
CREATE TABLE {{.Prefix}}delivery_projects (
 tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 code VARCHAR(16) COLLATE "C" NOT NULL, description VARCHAR(2000) NULL, client_entity_id BIGINT NULL,
 stage VARCHAR(32) COLLATE "C" NOT NULL DEFAULT 'INTAKE', stage_changed_at TIMESTAMPTZ(3) NOT NULL,
 blocked_kind VARCHAR(32) COLLATE "C" NULL, blocked_reason VARCHAR(2000) NULL,
 team_mode VARCHAR(32) COLLATE "C" NOT NULL DEFAULT 'HYBRID', team_mode_changed_at TIMESTAMPTZ(3) NULL, lead_agent_id BIGINT NULL,
 dispatch_policy VARCHAR(32) COLLATE "C" NOT NULL DEFAULT 'MANUAL', dispatch_policy_explicit BOOLEAN NOT NULL DEFAULT false,
 ensemble_max_branches INT NOT NULL DEFAULT 3, origin_intake_id BIGINT NULL, default_send_channel_id BIGINT NULL,
 budget_cap_micros BIGINT NULL, budget_cap_version INT NOT NULL DEFAULT 1, budget_spent_micros BIGINT NOT NULL DEFAULT 0,
 budget_notify_pct INT NOT NULL DEFAULT 80, budget_state VARCHAR(32) COLLATE "C" NOT NULL DEFAULT 'NORMAL',
 holds JSONB NOT NULL DEFAULT ('[]'), health VARCHAR(16) COLLATE "C" NOT NULL DEFAULT 'GREEN', health_reasons JSONB NOT NULL DEFAULT ('[]'), health_refreshed_at TIMESTAMPTZ(3) NULL,
 portal_settings JSONB NOT NULL DEFAULT ('{"show_ai_label":true,"show_work_item_titles":true,"show_evidence_summary":true,"show_release_notes":true,"contributor_display":"TEAM_ONLY","milestones_visible":true}'), acceptance_policy JSONB NOT NULL DEFAULT ('{"due_days":14,"reminder_days":[3,7,12],"escalate_day":14,"deemed_acceptance_days":null}'),
 next_delivery_seq INT NOT NULL DEFAULT 1, target_date DATE NULL,
 started_at TIMESTAMPTZ(3) NULL, delivered_at TIMESTAMPTZ(3) NULL, closed_at TIMESTAMPTZ(3) NULL, cancelled_at TIMESTAMPTZ(3) NULL, cancel_reason VARCHAR(2000) NULL,
 provisioning_issues JSONB NOT NULL DEFAULT ('[]'), archived_at TIMESTAMPTZ(3) NULL, version INT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL, created_by BIGINT NULL, updated_by BIGINT NULL,
 PRIMARY KEY (tenant_id, project_id),
 CONSTRAINT {{.Prefix}}uq_delivery_projects_code UNIQUE (tenant_id, code),
 CONSTRAINT {{.Prefix}}chk_delivery_project_code CHECK (code ~ '^[A-Z][A-Z0-9-]{1,15}$'),
 CONSTRAINT {{.Prefix}}chk_delivery_project_stage CHECK (stage IN ('INTAKE','DISCOVERY','PLANNING','ACTIVE','BLOCKED','ACCEPTANCE_PENDING','DELIVERED','CLOSED','CANCELLED')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_blocked CHECK (stage<>'BLOCKED' OR blocked_reason IS NOT NULL),
 CONSTRAINT {{.Prefix}}chk_delivery_project_blockkind CHECK (blocked_kind IS NULL OR blocked_kind IN ('CLIENT_CLARIFICATION','DEPENDENCY','EXTERNAL','OTHER')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_mode CHECK (team_mode IN ('HYBRID','AUTONOMOUS','HUMAN_ONLY')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_dispatch CHECK (dispatch_policy IN ('MANUAL','AUTO')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_branches CHECK (ensemble_max_branches BETWEEN 1 AND 5),
 CONSTRAINT {{.Prefix}}chk_delivery_project_cap CHECK (budget_cap_micros IS NULL OR budget_cap_micros>0),
 CONSTRAINT {{.Prefix}}chk_delivery_project_autonomy CHECK (team_mode<>'AUTONOMOUS' OR budget_cap_micros IS NOT NULL),
 CONSTRAINT {{.Prefix}}chk_delivery_project_spent CHECK (budget_spent_micros>=0),
 CONSTRAINT {{.Prefix}}chk_delivery_project_notify CHECK (budget_notify_pct BETWEEN 50 AND 95),
 CONSTRAINT {{.Prefix}}chk_delivery_project_budgetstate CHECK (budget_state IN ('NORMAL','NOTIFIED','EXHAUSTED')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_health CHECK (health IN ('GREEN','AMBER','RED','NONE')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_archive CHECK (archived_at IS NULL OR stage IN ('CLOSED','CANCELLED'))
);
CREATE INDEX {{.Prefix}}idx_delivery_projects_list ON {{.Prefix}}delivery_projects (tenant_id, archived_at, stage, health);
CREATE INDEX {{.Prefix}}idx_delivery_projects_client ON {{.Prefix}}delivery_projects (tenant_id, client_entity_id);
CREATE INDEX {{.Prefix}}idx_delivery_projects_lead ON {{.Prefix}}delivery_projects (tenant_id, lead_agent_id);
CREATE TABLE {{.Prefix}}delivery_project_contacts (
 tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, intake_contact_id BIGINT NOT NULL,
 role VARCHAR(32) COLLATE "C" NOT NULL DEFAULT 'CC', receives_deliveries BOOLEAN NOT NULL DEFAULT true, can_accept BOOLEAN NOT NULL DEFAULT true,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL, created_by BIGINT NULL, updated_by BIGINT NULL,
 PRIMARY KEY (tenant_id, project_id, intake_contact_id),
 CONSTRAINT {{.Prefix}}chk_delivery_contact_role CHECK (role IN ('PRIMARY','CC')),
 CONSTRAINT {{.Prefix}}chk_delivery_contact_accept CHECK (role<>'PRIMARY' OR can_accept=true)
);
CREATE TABLE {{.Prefix}}delivery_project_repositories (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL, name VARCHAR(100) NOT NULL,
 remote_url VARCHAR(2000) NOT NULL, default_branch VARCHAR(200) NOT NULL DEFAULT 'main', protected_branches JSONB NOT NULL DEFAULT ('["main"]'),
 is_primary BOOLEAN NOT NULL DEFAULT false, archived_at TIMESTAMPTZ(3) NULL, version INT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL, created_by BIGINT NULL, updated_by BIGINT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_repo_name UNIQUE (tenant_id,project_id,name)
);
CREATE UNIQUE INDEX {{.Prefix}}uq_delivery_contact_primary ON {{.Prefix}}delivery_project_contacts (tenant_id,project_id) WHERE role='PRIMARY';
CREATE UNIQUE INDEX {{.Prefix}}uq_delivery_repo_primary ON {{.Prefix}}delivery_project_repositories (tenant_id,project_id) WHERE is_primary=true;
-- +goose Down
DROP TABLE {{.Prefix}}delivery_project_repositories;
DROP TABLE {{.Prefix}}delivery_project_contacts;
DROP TABLE {{.Prefix}}delivery_projects;
