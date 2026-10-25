-- +goose Up
ALTER TABLE {{.Prefix}}intake_triage_cards MODIFY COLUMN change_id BIGINT NULL;
-- +goose Down
DELETE FROM {{.Prefix}}intake_triage_cards WHERE change_id IS NULL;
ALTER TABLE {{.Prefix}}intake_triage_cards MODIFY COLUMN change_id BIGINT NOT NULL;
