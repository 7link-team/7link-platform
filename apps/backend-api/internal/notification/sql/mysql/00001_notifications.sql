-- +goose Up
CREATE TABLE {{.Prefix}}notifications (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, recipient_principal_id BIGINT NOT NULL,
 type VARCHAR(96) NOT NULL, severity VARCHAR(8) NOT NULL, template_key VARCHAR(160) NOT NULL, params TEXT NOT NULL,
 link_path VARCHAR(512) NOT NULL, permission VARCHAR(96) NOT NULL, dedupe_key VARCHAR(160), dedupe_slot VARCHAR(160),
 merge_count INTEGER NOT NULL, read_at DATETIME(3), archived_at DATETIME(3), created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, last_merged_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}notifications_dedupe UNIQUE (tenant_id, recipient_principal_id, dedupe_slot)
);
CREATE INDEX {{.Prefix}}notifications_inbox ON {{.Prefix}}notifications(recipient_principal_id,tenant_id,archived_at,read_at,id);
CREATE INDEX {{.Prefix}}notifications_purge ON {{.Prefix}}notifications(read_at);
CREATE TABLE {{.Prefix}}notification_receipts(event_id BIGINT NOT NULL PRIMARY KEY, created_at DATETIME(3) NOT NULL);
CREATE TABLE {{.Prefix}}notification_preferences (
 tenant_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, type_key VARCHAR(96) NOT NULL, in_app BOOLEAN NOT NULL, browser_push BOOLEAN NOT NULL,
 email VARCHAR(16) NOT NULL, quiet_enabled BOOLEAN NOT NULL, quiet_start_min INTEGER NOT NULL, quiet_end_min INTEGER NOT NULL,
 timezone VARCHAR(64) NOT NULL, digest_hour INTEGER NOT NULL, version BIGINT NOT NULL, PRIMARY KEY(tenant_id,principal_id,type_key)
);
-- +goose Down
DROP TABLE {{.Prefix}}notification_preferences;
DROP TABLE {{.Prefix}}notification_receipts;
DROP TABLE {{.Prefix}}notifications;
