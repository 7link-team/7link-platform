-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_intake_threads (
 tenant_id BIGINT NOT NULL,
 intake_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 channel_id BIGINT NOT NULL,
 root_message_id BIGINT NOT NULL,
 recipient_id BIGINT NOT NULL,
 created_by BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,intake_id),
 CONSTRAINT {{.Prefix}}ck_intake_thread_ids CHECK (tenant_id>0 AND intake_id>0 AND project_id>0 AND channel_id>0 AND root_message_id>0 AND recipient_id>0 AND created_by>0)
);
CREATE INDEX {{.Prefix}}idx_intake_thread_project ON {{.Prefix}}clientwork_intake_threads (tenant_id,project_id,intake_id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_intake_threads;
