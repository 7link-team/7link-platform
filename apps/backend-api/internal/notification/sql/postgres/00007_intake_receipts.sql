-- +goose Up
CREATE TABLE {{.Prefix}}notification_intake_receipts (
    tenant_id BIGINT NOT NULL,
    intake_id BIGINT NOT NULL,
    recipient_principal_id BIGINT NOT NULL,
    project_id BIGINT NOT NULL,
    channel_id BIGINT NOT NULL,
    notification_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, intake_id, recipient_principal_id)
);
CREATE INDEX {{.Prefix}}idx_notification_intake_group ON {{.Prefix}}notification_intake_receipts (tenant_id, notification_id, recipient_principal_id);
-- +goose Down
DROP TABLE {{.Prefix}}notification_intake_receipts;
