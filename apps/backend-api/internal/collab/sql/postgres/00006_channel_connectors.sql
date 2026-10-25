-- +goose Up
CREATE TABLE {{.Prefix}}collab_channel_connectors (
 id BIGINT PRIMARY KEY, tenant_id BIGINT NOT NULL, channel_id BIGINT NOT NULL,
 created_by BIGINT NOT NULL, kind VARCHAR(16) NOT NULL, endpoint VARCHAR(512) NOT NULL,
 secret_sealed TEXT NOT NULL, enabled BOOLEAN NOT NULL, version INT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 UNIQUE (tenant_id, channel_id, kind)
);
CREATE INDEX {{.Prefix}}collab_connector_channel ON {{.Prefix}}collab_channel_connectors (tenant_id, channel_id, id);
CREATE TABLE {{.Prefix}}collab_channel_deliveries (
 id BIGINT PRIMARY KEY, tenant_id BIGINT NOT NULL, channel_id BIGINT NOT NULL,
 connector_id BIGINT NOT NULL, message_id BIGINT NOT NULL, config_version INT NOT NULL,
 state VARCHAR(16) NOT NULL, attempts INT NOT NULL, next_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 UNIQUE (connector_id, message_id)
);
CREATE INDEX {{.Prefix}}collab_delivery_due ON {{.Prefix}}collab_channel_deliveries (tenant_id, state, next_at);
-- +goose Down
DROP TABLE {{.Prefix}}collab_channel_deliveries;
DROP TABLE {{.Prefix}}collab_channel_connectors;
