-- +goose Up
CREATE TABLE {{.Prefix}}delivery_projects (
 tenant_id INTEGER NOT NULL, project_id INTEGER NOT NULL,
 code TEXT NOT NULL, description VARCHAR(2000) NULL CHECK (description IS NULL OR length(description)<=2000), client_entity_id INTEGER NULL,
 stage VARCHAR(32) NOT NULL DEFAULT 'INTAKE' CHECK (stage IS NULL OR length(stage)<=32), stage_changed_at DATETIME NOT NULL,
 blocked_kind VARCHAR(32) NULL CHECK (blocked_kind IS NULL OR length(blocked_kind)<=32), blocked_reason VARCHAR(2000) NULL CHECK (blocked_reason IS NULL OR length(blocked_reason)<=2000),
 team_mode VARCHAR(32) NOT NULL DEFAULT 'HYBRID' CHECK (team_mode IS NULL OR length(team_mode)<=32), team_mode_changed_at DATETIME NULL, lead_agent_id INTEGER NULL,
 dispatch_policy VARCHAR(32) NOT NULL DEFAULT 'MANUAL' CHECK (dispatch_policy IS NULL OR length(dispatch_policy)<=32), dispatch_policy_explicit BOOLEAN NOT NULL DEFAULT false,
 ensemble_max_branches INT NOT NULL DEFAULT 3, origin_intake_id INTEGER NULL, default_send_channel_id INTEGER NULL,
 budget_cap_micros INTEGER NULL, budget_cap_version INT NOT NULL DEFAULT 1, budget_spent_micros INTEGER NOT NULL DEFAULT 0,
 budget_notify_pct INT NOT NULL DEFAULT 80, budget_state VARCHAR(32) NOT NULL DEFAULT 'NORMAL' CHECK (budget_state IS NULL OR length(budget_state)<=32),
 holds TEXT NOT NULL DEFAULT ('[]'), health VARCHAR(16) NOT NULL DEFAULT 'GREEN' CHECK (health IS NULL OR length(health)<=16), health_reasons TEXT NOT NULL DEFAULT ('[]'), health_refreshed_at DATETIME NULL,
 portal_settings TEXT NOT NULL DEFAULT ('{"show_ai_label":true,"show_work_item_titles":true,"show_evidence_summary":true,"show_release_notes":true,"contributor_display":"TEAM_ONLY","milestones_visible":true}'), acceptance_policy TEXT NOT NULL DEFAULT ('{"due_days":14,"reminder_days":[3,7,12],"escalate_day":14,"deemed_acceptance_days":null}'),
 next_delivery_seq INT NOT NULL DEFAULT 1, target_date TEXT NULL,
 started_at DATETIME NULL, delivered_at DATETIME NULL, closed_at DATETIME NULL, cancelled_at DATETIME NULL, cancel_reason VARCHAR(2000) NULL CHECK (cancel_reason IS NULL OR length(cancel_reason)<=2000),
 provisioning_issues TEXT NOT NULL DEFAULT ('[]'), archived_at DATETIME NULL, version INT NOT NULL DEFAULT 1,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, created_by INTEGER NULL, updated_by INTEGER NULL,
 CHECK (json_valid(holds) AND json_valid(health_reasons) AND json_valid(portal_settings) AND json_valid(acceptance_policy) AND json_valid(provisioning_issues)),
 CHECK (target_date IS NULL OR (length(target_date)=10 AND strftime('%Y-%m-%d', julianday(target_date)) IS NOT NULL AND strftime('%Y-%m-%d', julianday(target_date))=target_date)),
 PRIMARY KEY (tenant_id, project_id),
 CONSTRAINT {{.Prefix}}uq_delivery_projects_code UNIQUE (tenant_id, code),
 CONSTRAINT {{.Prefix}}chk_delivery_project_code CHECK (length(code) BETWEEN 2 AND 16 AND substr(code,1,1) GLOB '[A-Z]' AND code NOT GLOB '*[^A-Z0-9-]*'),
 CONSTRAINT {{.Prefix}}chk_delivery_project_stage CHECK (stage IN ('INTAKE','DISCOVERY','PLANNING','ACTIVE','BLOCKED','ACCEPTANCE_PENDING','DELIVERED','CLOSED','CANCELLED')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_blocked CHECK (stage<>'BLOCKED' OR blocked_reason IS NOT NULL),
 CONSTRAINT {{.Prefix}}chk_delivery_project_blockkind CHECK (blocked_kind IS NULL OR blocked_kind IN ('CLIENT_CLARIFICATION','DEPENDENCY','EXTERNAL','OTHER')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_mode CHECK (team_mode IN ('HYBRID','AUTONOMOUS','HUMAN_ONLY')),
 CONSTRAINT {{.Prefix}}chk_delivery_project_explicit CHECK (dispatch_policy_explicit IN (0,1)),
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
 tenant_id INTEGER NOT NULL, project_id INTEGER NOT NULL, intake_contact_id INTEGER NOT NULL,
 role VARCHAR(32) NOT NULL DEFAULT 'CC' CHECK (role IS NULL OR length(role)<=32), receives_deliveries BOOLEAN NOT NULL DEFAULT true, can_accept BOOLEAN NOT NULL DEFAULT true,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, created_by INTEGER NULL, updated_by INTEGER NULL,
 PRIMARY KEY (tenant_id, project_id, intake_contact_id),
 CONSTRAINT {{.Prefix}}chk_delivery_contact_booleans CHECK (receives_deliveries IN (0,1) AND can_accept IN (0,1)),
 CONSTRAINT {{.Prefix}}chk_delivery_contact_role CHECK (role IN ('PRIMARY','CC')),
 CONSTRAINT {{.Prefix}}chk_delivery_contact_accept CHECK (role<>'PRIMARY' OR can_accept=true)
);
CREATE TABLE {{.Prefix}}delivery_project_repositories (
 tenant_id INTEGER NOT NULL, id INTEGER NOT NULL, project_id INTEGER NOT NULL, name VARCHAR(100) NOT NULL CHECK (name IS NULL OR length(name)<=100),
 remote_url VARCHAR(2000) NOT NULL CHECK (remote_url IS NULL OR length(remote_url)<=2000), default_branch VARCHAR(200) NOT NULL DEFAULT 'main' CHECK (default_branch IS NULL OR length(default_branch)<=200), protected_branches TEXT NOT NULL DEFAULT ('["main"]'),
 is_primary BOOLEAN NOT NULL DEFAULT false, archived_at DATETIME NULL, version INT NOT NULL DEFAULT 1,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, created_by INTEGER NULL, updated_by INTEGER NULL,
 CHECK (json_valid(protected_branches)),
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}chk_delivery_repo_primary CHECK (is_primary IN (0,1)),
 CONSTRAINT {{.Prefix}}uq_delivery_repo_name UNIQUE (tenant_id,project_id,name)
);
CREATE UNIQUE INDEX {{.Prefix}}uq_delivery_contact_primary ON {{.Prefix}}delivery_project_contacts (tenant_id,project_id) WHERE role='PRIMARY';
CREATE UNIQUE INDEX {{.Prefix}}uq_delivery_repo_primary ON {{.Prefix}}delivery_project_repositories (tenant_id,project_id) WHERE is_primary=true;
-- +goose Down
DROP TABLE {{.Prefix}}delivery_project_repositories;
DROP TABLE {{.Prefix}}delivery_project_contacts;
DROP TABLE {{.Prefix}}delivery_projects;
