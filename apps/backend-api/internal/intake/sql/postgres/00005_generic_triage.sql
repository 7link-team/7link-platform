-- +goose Up
ALTER TABLE {{.Prefix}}intake_triage_cards ALTER COLUMN change_id DROP NOT NULL;
-- +goose Down
DELETE FROM {{.Prefix}}intake_triage_cards WHERE change_id IS NULL;
ALTER TABLE {{.Prefix}}intake_triage_cards ALTER COLUMN change_id SET NOT NULL;
