-- +goose Up
ALTER TABLE {{.Prefix}}domain_event_cursors ADD COLUMN replay_of VARCHAR(96) NULL;
ALTER TABLE {{.Prefix}}domain_event_cursors ADD COLUMN replay_to_seq BIGINT NULL;
ALTER TABLE {{.Prefix}}domain_event_cursors ADD COLUMN replay_ignore_receipts BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE {{.Prefix}}domain_event_receipts (
 id BIGINT NOT NULL PRIMARY KEY, consumer VARCHAR(128) NOT NULL,
 event_id BIGINT NOT NULL, tenant_id BIGINT NOT NULL, processed_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_de_receipt UNIQUE(consumer,event_id)
);
CREATE INDEX {{.Prefix}}idx_de_receipt_purge ON {{.Prefix}}domain_event_receipts(processed_at);
CREATE TABLE {{.Prefix}}domain_event_parked (
 id BIGINT NOT NULL PRIMARY KEY, consumer VARCHAR(128) NOT NULL, partition_no SMALLINT NOT NULL,
 event_id BIGINT NOT NULL, stream_seq BIGINT NOT NULL, tenant_id BIGINT NOT NULL,
 subject_kind VARCHAR(64) NOT NULL, subject_id BIGINT NOT NULL,
 reason VARCHAR(16) NOT NULL, state VARCHAR(16) NOT NULL DEFAULT 'PARKED', attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at_ms BIGINT NULL, last_error_code VARCHAR(64) NOT NULL DEFAULT '', last_error VARCHAR(2000) NOT NULL DEFAULT '',
 resolved_by BIGINT NULL, resolution_note VARCHAR(2000) NOT NULL DEFAULT '',
 created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, resolved_at DATETIME(3) NULL, version INTEGER NOT NULL DEFAULT 1,
 CONSTRAINT {{.Prefix}}uq_de_parked UNIQUE(consumer,event_id),
 CONSTRAINT {{.Prefix}}chk_de_parked CHECK(partition_no BETWEEN 0 AND 15 AND attempts>=0 AND version>=1 AND stream_seq>0 AND reason IN ('FAILED','BLOCKED') AND state IN ('PARKED','REDELIVERING','DELIVERED','DISCARDED'))
);
CREATE INDEX {{.Prefix}}idx_de_parked_subject ON {{.Prefix}}domain_event_parked(consumer,subject_kind,subject_id,state,stream_seq);
CREATE INDEX {{.Prefix}}idx_de_parked_due ON {{.Prefix}}domain_event_parked(state,next_attempt_at_ms);
-- +goose Down
ALTER TABLE {{.Prefix}}domain_event_cursors DROP COLUMN replay_ignore_receipts;
ALTER TABLE {{.Prefix}}domain_event_cursors DROP COLUMN replay_to_seq;
ALTER TABLE {{.Prefix}}domain_event_cursors DROP COLUMN replay_of;
DROP TABLE {{.Prefix}}domain_event_parked;
DROP TABLE {{.Prefix}}domain_event_receipts;
