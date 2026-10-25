-- +goose Up
CREATE TABLE {{.Prefix}}notification_deliveries (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, notification_id BIGINT, channel VARCHAR(16) NOT NULL,
 recipient_principal_id BIGINT, recipient_address_hash VARCHAR(64) NOT NULL, recipient_address_sealed TEXT NOT NULL, payload_sealed TEXT NOT NULL,
 message_id VARCHAR(998) NOT NULL, source_ref VARCHAR(160) NOT NULL, state VARCHAR(16) NOT NULL, suppress_reason VARCHAR(32) NOT NULL,
 job_id BIGINT, attempts INTEGER NOT NULL, last_error VARCHAR(1024) NOT NULL, sent_at DATETIME, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}notification_deliveries_channel UNIQUE(notification_id,channel),
 CONSTRAINT {{.Prefix}}notification_deliveries_state_check CHECK(state IN('PENDING','SENDING','SENT','FAILED','SUPPRESSED'))
);
CREATE INDEX {{.Prefix}}notification_deliveries_rate ON {{.Prefix}}notification_deliveries(recipient_address_hash,channel,created_at);
CREATE INDEX {{.Prefix}}notification_deliveries_state ON {{.Prefix}}notification_deliveries(state,created_at);
-- +goose Down
DROP TABLE {{.Prefix}}notification_deliveries;
