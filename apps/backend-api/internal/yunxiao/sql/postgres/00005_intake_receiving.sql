-- +goose Up
ALTER TABLE {{.Prefix}}intake_channel_settings ADD COLUMN credential_hash VARCHAR(64) NULL;
ALTER TABLE {{.Prefix}}intake_channel_settings ADD COLUMN receiving_day VARCHAR(10) NULL;
ALTER TABLE {{.Prefix}}intake_channel_settings ADD COLUMN received_count INT NOT NULL DEFAULT 0;
-- +goose Down
ALTER TABLE {{.Prefix}}intake_channel_settings DROP COLUMN received_count;
ALTER TABLE {{.Prefix}}intake_channel_settings DROP COLUMN receiving_day;
ALTER TABLE {{.Prefix}}intake_channel_settings DROP COLUMN credential_hash;
