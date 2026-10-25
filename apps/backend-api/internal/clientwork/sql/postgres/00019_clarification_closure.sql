-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_state;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_state CHECK (state IN ('OPEN','ANSWERED','CANCELLED'));
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN closed_at TIMESTAMPTZ(3);
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN close_reason VARCHAR(32);
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN close_note VARCHAR(2000);
UPDATE {{.Prefix}}clientwork_clarification_rounds SET closed_at=updated_at,close_reason='ALL_ANSWERED' WHERE state='ANSWERED';
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_closure CHECK ((state='OPEN' AND closed_at IS NULL AND close_reason IS NULL AND close_note IS NULL) OR (state='ANSWERED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='ALL_ANSWERED') OR (state='CANCELLED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='CANCELLED_BY_TEAM' AND close_note IS NOT NULL AND char_length(close_note) BETWEEN 1 AND 2000));
-- +goose Down
-- Existing cancellations prevent rollback; no history is silently rewritten.
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_rollback CHECK (state IN ('OPEN','ANSWERED'));
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_closure;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_state;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_state CHECK (state IN ('OPEN','ANSWERED'));
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_rollback;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN close_reason;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN close_note;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN closed_at;
