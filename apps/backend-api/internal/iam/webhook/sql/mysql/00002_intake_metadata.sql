-- +goose Up
ALTER TABLE {{.Prefix}}iam_webhook_subscriptions ADD COLUMN payload_mode VARCHAR(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT 'audit';
ALTER TABLE {{.Prefix}}iam_webhook_subscriptions ADD CONSTRAINT {{.Prefix}}chk_iam_webhook_payload_mode CHECK (payload_mode IN ('audit', 'intake_metadata'));
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}webhook_payload_rollback_guard;
CREATE TABLE {{.Prefix}}webhook_payload_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}webhook_payload_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}iam_webhook_subscriptions WHERE payload_mode <> 'audit';
DROP TABLE {{.Prefix}}webhook_payload_rollback_guard;
ALTER TABLE {{.Prefix}}iam_webhook_subscriptions DROP CHECK {{.Prefix}}chk_iam_webhook_payload_mode;
ALTER TABLE {{.Prefix}}iam_webhook_subscriptions DROP COLUMN payload_mode;
