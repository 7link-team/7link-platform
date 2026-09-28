-- Historical recipients cannot safely be matched to present-day email owners.
-- NULL identifies legacy rows requiring explicit retention remediation.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_notification_outbox ADD COLUMN principal_id BIGINT NULL;
CREATE INDEX {{.Prefix}}idx_iam_notification_principal
    ON {{.Prefix}}iam_notification_outbox (principal_id, status);

-- +goose Down
DROP INDEX {{.Prefix}}idx_iam_notification_principal ON {{.Prefix}}iam_notification_outbox;
ALTER TABLE {{.Prefix}}iam_notification_outbox DROP COLUMN principal_id;
