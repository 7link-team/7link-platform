-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_rework_dispositions (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 intake_id BIGINT NOT NULL,
 receipt_id BIGINT NOT NULL,
 item_id VARCHAR(128) NOT NULL,
 mode VARCHAR(32) NOT NULL,
 target_id BIGINT NULL,
 reason TEXT NOT NULL,
 client_message TEXT NOT NULL,
 actor_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_rework_disposition UNIQUE(tenant_id,intake_id,item_id,mode),
 CONSTRAINT {{.Prefix}}ck_rework_disposition_mode CHECK(mode IN ('REOPEN_WORK_ITEMS','APPEND_TO_REQUIREMENT','NEW_REQUIREMENT','DECLINE')),
 CONSTRAINT {{.Prefix}}ck_rework_disposition_target CHECK((mode='DECLINE' AND target_id IS NULL) OR (mode<>'DECLINE' AND target_id IS NOT NULL))
);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_rework_dispositions;
