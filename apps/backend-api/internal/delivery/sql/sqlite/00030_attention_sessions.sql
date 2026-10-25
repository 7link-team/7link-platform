-- +goose Up
CREATE TABLE {{.Prefix}}delivery_attention_sessions (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL, claim_id BIGINT NOT NULL, principal_id BIGINT NOT NULL,
 surface VARCHAR(20) NOT NULL CHECK (surface='REVIEW_DESK'),
 started_at DATETIME NOT NULL, last_heartbeat_at DATETIME NOT NULL, ended_at DATETIME NULL,
 active_ms BIGINT NOT NULL DEFAULT 0 CHECK (active_ms BETWEEN 0 AND 14400000),
 last_sequence BIGINT NOT NULL DEFAULT 0 CHECK (last_sequence >= 0), decision_id BIGINT NULL,
 PRIMARY KEY (tenant_id,id),
 FOREIGN KEY (tenant_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items(tenant_id,id),
 FOREIGN KEY (tenant_id,claim_id) REFERENCES {{.Prefix}}delivery_result_versions(tenant_id,id)
);
CREATE INDEX {{.Prefix}}delivery_attention_claim_idx ON {{.Prefix}}delivery_attention_sessions(tenant_id,claim_id,principal_id);
CREATE INDEX {{.Prefix}}delivery_attention_stale_idx ON {{.Prefix}}delivery_attention_sessions(tenant_id,ended_at,last_heartbeat_at);
ALTER TABLE {{.Prefix}}delivery_result_actions ADD COLUMN attention_ms BIGINT NULL;

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_attention_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_attention_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_attention_sessions;
INSERT INTO {{.Prefix}}delivery_attention_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_result_actions WHERE attention_ms IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_attention_rollback_guard;
ALTER TABLE {{.Prefix}}delivery_result_actions DROP COLUMN attention_ms;
DROP TABLE {{.Prefix}}delivery_attention_sessions;
