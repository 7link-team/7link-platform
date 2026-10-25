-- +goose Up
CREATE TABLE {{.Prefix}}notification_source_receipts (
    tenant_id BIGINT NOT NULL,
    type_key VARCHAR(96) NOT NULL,
    occurrence_id BIGINT NOT NULL,
    recipient_principal_id BIGINT NOT NULL,
    source_id BIGINT NOT NULL,
    notification_id BIGINT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, type_key, occurrence_id, recipient_principal_id)
);
CREATE INDEX {{.Prefix}}idx_notification_source_group ON {{.Prefix}}notification_source_receipts (tenant_id, notification_id, recipient_principal_id);
-- +goose Down
DROP TABLE {{.Prefix}}notification_source_receipts;
