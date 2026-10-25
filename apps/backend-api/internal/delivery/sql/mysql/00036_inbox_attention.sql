-- +goose Up
CREATE TABLE {{.Prefix}}delivery_attention_sessions_next (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL, claim_id BIGINT NULL, interaction_id BIGINT NULL, principal_id BIGINT NOT NULL,
 surface VARCHAR(20) NOT NULL CHECK (surface IN ('REVIEW_DESK','INBOX')),
 started_at DATETIME(3) NOT NULL, last_heartbeat_at DATETIME(3) NOT NULL, ended_at DATETIME(3) NULL,
 active_ms BIGINT NOT NULL DEFAULT 0 CHECK (active_ms BETWEEN 0 AND 14400000),
 last_sequence BIGINT NOT NULL DEFAULT 0 CHECK (last_sequence >= 0), decision_id BIGINT NULL,
 CONSTRAINT {{.Prefix}}delivery_attention_source CHECK ((claim_id IS NOT NULL AND interaction_id IS NULL) OR (claim_id IS NULL AND interaction_id IS NOT NULL AND surface='INBOX')),
 PRIMARY KEY (tenant_id,id),
 FOREIGN KEY (tenant_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items(tenant_id,id),
 FOREIGN KEY (tenant_id,claim_id) REFERENCES {{.Prefix}}delivery_result_versions(tenant_id,id)
);
INSERT INTO {{.Prefix}}delivery_attention_sessions_next(tenant_id,id,project_id,work_item_id,claim_id,principal_id,surface,started_at,last_heartbeat_at,ended_at,active_ms,last_sequence,decision_id) SELECT tenant_id,id,project_id,work_item_id,claim_id,principal_id,surface,started_at,last_heartbeat_at,ended_at,active_ms,last_sequence,decision_id FROM {{.Prefix}}delivery_attention_sessions;
DROP TABLE {{.Prefix}}delivery_attention_sessions;
RENAME TABLE {{.Prefix}}delivery_attention_sessions_next TO {{.Prefix}}delivery_attention_sessions;
CREATE INDEX {{.Prefix}}delivery_attention_claim_idx ON {{.Prefix}}delivery_attention_sessions(tenant_id,claim_id,principal_id);
CREATE INDEX {{.Prefix}}delivery_attention_stale_idx ON {{.Prefix}}delivery_attention_sessions(tenant_id,ended_at,last_heartbeat_at);

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_inbox_attention_rollback_guard(n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}delivery_inbox_attention_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_attention_sessions WHERE surface='INBOX' OR interaction_id IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_inbox_attention_rollback_guard;
CREATE TABLE {{.Prefix}}delivery_attention_sessions_next (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL, claim_id BIGINT NOT NULL, principal_id BIGINT NOT NULL,
 surface VARCHAR(20) NOT NULL CHECK (surface IN ('REVIEW_DESK')),
 started_at DATETIME(3) NOT NULL, last_heartbeat_at DATETIME(3) NOT NULL, ended_at DATETIME(3) NULL,
 active_ms BIGINT NOT NULL DEFAULT 0 CHECK (active_ms BETWEEN 0 AND 14400000),
 last_sequence BIGINT NOT NULL DEFAULT 0 CHECK (last_sequence >= 0), decision_id BIGINT NULL,
 PRIMARY KEY (tenant_id,id),
 FOREIGN KEY (tenant_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items(tenant_id,id),
 FOREIGN KEY (tenant_id,claim_id) REFERENCES {{.Prefix}}delivery_result_versions(tenant_id,id)
);
INSERT INTO {{.Prefix}}delivery_attention_sessions_next(tenant_id,id,project_id,work_item_id,claim_id,principal_id,surface,started_at,last_heartbeat_at,ended_at,active_ms,last_sequence,decision_id) SELECT tenant_id,id,project_id,work_item_id,claim_id,principal_id,surface,started_at,last_heartbeat_at,ended_at,active_ms,last_sequence,decision_id FROM {{.Prefix}}delivery_attention_sessions;
DROP TABLE {{.Prefix}}delivery_attention_sessions;
RENAME TABLE {{.Prefix}}delivery_attention_sessions_next TO {{.Prefix}}delivery_attention_sessions;
CREATE INDEX {{.Prefix}}delivery_attention_claim_idx ON {{.Prefix}}delivery_attention_sessions(tenant_id,claim_id,principal_id);
CREATE INDEX {{.Prefix}}delivery_attention_stale_idx ON {{.Prefix}}delivery_attention_sessions(tenant_id,ended_at,last_heartbeat_at);
