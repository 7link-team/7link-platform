-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_package_sends (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,package_id BIGINT NOT NULL,
 package_version INT NOT NULL,content_hash VARCHAR(64) NOT NULL,recipient_id BIGINT NOT NULL,
 channel_id BIGINT NOT NULL,attempt_no INT NOT NULL,state VARCHAR(16) NOT NULL,
 message_id BIGINT,failure_code VARCHAR(64),actor_id BIGINT NOT NULL,created_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_clientwork_package_send UNIQUE(tenant_id,package_id,attempt_no),
 CONSTRAINT {{.Prefix}}fk_clientwork_package_send FOREIGN KEY(tenant_id,project_id,package_id) REFERENCES {{.Prefix}}clientwork_packages(tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (tenant_id>0 AND id>0 AND project_id>0 AND package_id>0 AND recipient_id>0 AND channel_id>0 AND actor_id>0 AND attempt_no>0 AND package_version>0 AND LENGTH(content_hash)=64),
 CHECK ((state='SUCCEEDED' AND message_id IS NOT NULL AND message_id>0 AND failure_code IS NULL) OR (state='FAILED' AND message_id IS NULL AND failure_code IS NOT NULL))
);
CREATE INDEX {{.Prefix}}idx_clientwork_package_send ON {{.Prefix}}clientwork_package_sends(tenant_id,project_id,package_id,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}package_send_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}package_send_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}clientwork_package_sends;
DROP TABLE {{.Prefix}}package_send_rollback_guard;
DROP TABLE {{.Prefix}}clientwork_package_sends;
