-- +goose Up
CREATE TABLE {{.Prefix}}delivery_project_budget_events (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 kind VARCHAR(32) NOT NULL,
 threshold_lock VARCHAR(32),
 threshold_pct INT,
 cap_before_micros BIGINT,
 authority_source VARCHAR(128) NOT NULL,
 cap_micros BIGINT,
 spent_micros BIGINT NOT NULL,
 notify_pct INT NOT NULL,
 cap_version INT NOT NULL,
 state VARCHAR(16) NOT NULL,
 reason VARCHAR(2000) NOT NULL,
 actor_id BIGINT,
 occurred_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_budget_threshold UNIQUE (tenant_id,project_id,cap_version,threshold_lock),
 CHECK ((kind IN ('THRESHOLD_NOTIFY','THRESHOLD_EXHAUSTED') AND threshold_lock=kind AND threshold_lock IS NOT NULL) OR (kind NOT IN ('THRESHOLD_NOTIFY','THRESHOLD_EXHAUSTED') AND threshold_lock IS NULL)),
 CONSTRAINT {{.Prefix}}fk_delivery_budget_event_project FOREIGN KEY (tenant_id,project_id) REFERENCES {{.Prefix}}delivery_projects (tenant_id,project_id) ON DELETE RESTRICT,
 CHECK (spent_micros>=0 AND cap_version>=1 AND notify_pct BETWEEN 50 AND 95),
 CHECK (cap_micros IS NULL OR cap_micros>0),
 CHECK (tenant_id>0 AND id>0 AND project_id>0),
 CHECK (kind IN ('CAP_CHANGED','THRESHOLD_NOTIFY','THRESHOLD_EXHAUSTED','HOLD_RELEASED')),
 CHECK (state IN ('NORMAL','NOTIFIED','EXHAUSTED')),
 CHECK ((kind='THRESHOLD_NOTIFY' AND threshold_pct=notify_pct AND threshold_pct IS NOT NULL) OR (kind='THRESHOLD_EXHAUSTED' AND threshold_pct=100 AND threshold_pct IS NOT NULL) OR (kind IN ('CAP_CHANGED','HOLD_RELEASED') AND threshold_pct IS NULL))
);
CREATE INDEX {{.Prefix}}idx_delivery_budget_event_project ON {{.Prefix}}delivery_project_budget_events (tenant_id,project_id,occurred_at,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_budget_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_budget_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_project_budget_events;
DROP TABLE {{.Prefix}}delivery_budget_rollback_guard;
DROP TABLE {{.Prefix}}delivery_project_budget_events;
