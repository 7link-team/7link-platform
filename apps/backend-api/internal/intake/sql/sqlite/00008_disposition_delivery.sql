-- +goose Up
ALTER TABLE {{.Prefix}}intake_records ADD COLUMN client_notification_state VARCHAR(32) NOT NULL DEFAULT 'NOT_REQUESTED' CHECK(client_notification_state IN ('NOT_REQUESTED', 'UNAVAILABLE', 'POSTED'));
ALTER TABLE {{.Prefix}}intake_records ADD COLUMN client_notification_message_id BIGINT NULL CHECK((client_notification_state = 'POSTED' AND client_notification_message_id IS NOT NULL AND client_notification_message_id > 0) OR (client_notification_state IN ('NOT_REQUESTED', 'UNAVAILABLE') AND client_notification_message_id IS NULL));

-- +goose Down
ALTER TABLE {{.Prefix}}intake_records DROP COLUMN client_notification_message_id;
ALTER TABLE {{.Prefix}}intake_records DROP COLUMN client_notification_state;
