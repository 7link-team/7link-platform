-- +goose Up
CREATE TABLE {{.Prefix}}notification_rotation_alerts(tenant_id BIGINT NOT NULL, rotation_id BIGINT NOT NULL, condition_key VARCHAR(8) NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL, PRIMARY KEY(tenant_id,rotation_id,condition_key));
-- +goose Down
DROP TABLE {{.Prefix}}notification_rotation_alerts;
