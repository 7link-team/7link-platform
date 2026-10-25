-- +goose Up
CREATE TABLE {{.Prefix}}intake_triage_cards (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 intake_id BIGINT NOT NULL,
 revision INT NOT NULL,
 change_id BIGINT NOT NULL,
 proposal_hash VARCHAR(64) NOT NULL,
 proposal_json TEXT NOT NULL,
 impact_json TEXT NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_intake_card_revision UNIQUE (tenant_id,intake_id,revision),
 CONSTRAINT {{.Prefix}}ck_intake_card_revision CHECK (revision > 0),
 CONSTRAINT {{.Prefix}}ck_intake_card_hash CHECK (length(proposal_hash) = 64)
);
CREATE INDEX {{.Prefix}}idx_intake_card_change ON {{.Prefix}}intake_triage_cards (tenant_id,change_id,intake_id);
-- +goose Down
DROP TABLE {{.Prefix}}intake_triage_cards;
