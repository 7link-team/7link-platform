-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_state;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_closure;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_state CHECK (state IN ('OPEN','ANSWERED','CANCELLED','EXPIRED'));
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_closure CHECK ((state='EXPIRED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='DEADLINE_ELAPSED' AND close_note IS NULL) OR (state='OPEN' AND closed_at IS NULL AND close_reason IS NULL AND close_note IS NULL) OR (state='ANSWERED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='ALL_ANSWERED') OR (state='CANCELLED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='CANCELLED_BY_TEAM' AND close_note IS NOT NULL AND char_length(close_note) BETWEEN 1 AND 2000));
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN channel_id BIGINT;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN channel_version INT;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN reminder_hours INT;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN max_reminders INT;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN due_at TIMESTAMPTZ(3);
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN next_reminder_at TIMESTAMPTZ(3);
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD COLUMN reminders_sent INT NOT NULL DEFAULT 0;
CREATE INDEX {{.Prefix}}idx_clarification_due ON {{.Prefix}}clientwork_clarification_rounds (tenant_id,state,due_at,next_reminder_at);
-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_timing_rollback CHECK (state IN ('OPEN','ANSWERED','CANCELLED'));
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_state;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_closure;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_state CHECK (state IN ('OPEN','ANSWERED','CANCELLED'));
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds ADD CONSTRAINT {{.Prefix}}ck_clarification_closure CHECK ((state='OPEN' AND closed_at IS NULL AND close_reason IS NULL AND close_note IS NULL) OR (state='ANSWERED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='ALL_ANSWERED') OR (state='CANCELLED' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND close_reason='CANCELLED_BY_TEAM' AND close_note IS NOT NULL AND char_length(close_note) BETWEEN 1 AND 2000));
DROP INDEX {{.Prefix}}idx_clarification_due;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN channel_id;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN channel_version;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN reminder_hours;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN max_reminders;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN due_at;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN next_reminder_at;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP COLUMN reminders_sent;
ALTER TABLE {{.Prefix}}clientwork_clarification_rounds DROP CONSTRAINT {{.Prefix}}ck_clarification_timing_rollback;
