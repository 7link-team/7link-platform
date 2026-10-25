-- +goose Up
ALTER TABLE {{.Prefix}}intake_records ADD COLUMN last_triage_requested_at DATETIME NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}intake_records DROP COLUMN last_triage_requested_at;
