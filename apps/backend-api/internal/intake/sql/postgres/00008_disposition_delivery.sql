-- +goose Up
ALTER TABLE {{.Prefix}}intake_records ADD COLUMN client_notification_state VARCHAR(32) NOT NULL DEFAULT 'NOT_REQUESTED';
ALTER TABLE {{.Prefix}}intake_records ADD COLUMN client_notification_message_id BIGINT NULL;
ALTER TABLE {{.Prefix}}intake_records ADD CONSTRAINT {{.Prefix}}ck_intake_disposition_delivery CHECK((client_notification_state = 'POSTED' AND client_notification_message_id IS NOT NULL AND client_notification_message_id > 0) OR (client_notification_state IN ('NOT_REQUESTED', 'UNAVAILABLE') AND client_notification_message_id IS NULL));

-- +goose Down
ALTER TABLE {{.Prefix}}intake_records DROP CONSTRAINT {{.Prefix}}ck_intake_disposition_delivery;
ALTER TABLE {{.Prefix}}intake_records DROP COLUMN client_notification_message_id;
ALTER TABLE {{.Prefix}}intake_records DROP COLUMN client_notification_state;
