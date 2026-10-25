-- +goose Up
CREATE TABLE {{.Prefix}}notification_dispatch_receipts (
    tenant_id BIGINT NOT NULL,
    proposal_id BIGINT NOT NULL,
    recipient_principal_id BIGINT NOT NULL,
    project_id BIGINT NOT NULL,
    notification_id BIGINT NOT NULL,
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (tenant_id, proposal_id, recipient_principal_id)
);
CREATE INDEX {{.Prefix}}idx_notification_dispatch_group ON {{.Prefix}}notification_dispatch_receipts (tenant_id, notification_id, recipient_principal_id);
-- +goose Down
DROP TABLE {{.Prefix}}notification_dispatch_receipts;
