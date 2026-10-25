-- +goose Up
ALTER TABLE {{.Prefix}}agents_live_states ADD COLUMN wake_mode VARCHAR(16) NOT NULL DEFAULT 'CHANNEL';
ALTER TABLE {{.Prefix}}agents_live_states ADD COLUMN wake_url VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}agents_live_states ADD COLUMN wake_secret_sealed VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}agents_live_states ADD COLUMN wake_version INT NOT NULL DEFAULT 1;
ALTER TABLE {{.Prefix}}agents_live_states ADD COLUMN wake_failures INT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}agents_live_states ADD COLUMN last_wake_seq BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}agents_live_states ADD COLUMN next_wake_at DATETIME NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}agents_live_states DROP COLUMN next_wake_at;
ALTER TABLE {{.Prefix}}agents_live_states DROP COLUMN last_wake_seq;
ALTER TABLE {{.Prefix}}agents_live_states DROP COLUMN wake_failures;
ALTER TABLE {{.Prefix}}agents_live_states DROP COLUMN wake_version;
ALTER TABLE {{.Prefix}}agents_live_states DROP COLUMN wake_secret_sealed;
ALTER TABLE {{.Prefix}}agents_live_states DROP COLUMN wake_url;
ALTER TABLE {{.Prefix}}agents_live_states DROP COLUMN wake_mode;
