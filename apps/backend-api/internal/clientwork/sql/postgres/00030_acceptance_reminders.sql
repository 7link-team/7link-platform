-- +goose Up
CREATE UNIQUE INDEX {{.Prefix}}uq_clientwork_acceptance_pkg ON {{.Prefix}}clientwork_packages(tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}clientwork_acceptance_reminders (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,package_id BIGINT NOT NULL,
 package_version INT NOT NULL,content_hash VARCHAR(64) NOT NULL,recipient_id BIGINT NOT NULL,
 day_no INT NOT NULL,kind VARCHAR(16) NOT NULL,
 published_at TIMESTAMPTZ(3) NOT NULL,due_at TIMESTAMPTZ(3) NOT NULL,emitted_at TIMESTAMPTZ(3),cancelled_at TIMESTAMPTZ(3),
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_clientwork_acceptance_reminder UNIQUE(tenant_id,package_id,package_version,day_no),
 CONSTRAINT {{.Prefix}}fk_clientwork_acceptance_reminder_package FOREIGN KEY(tenant_id,project_id,package_id) REFERENCES {{.Prefix}}clientwork_packages(tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (package_version>0 AND day_no IN (3,7,12,14)),
 CHECK ((day_no=14 AND kind='ESCALATION') OR (day_no<>14 AND kind='REMINDER')),
 CHECK (tenant_id>0 AND id>0 AND project_id>0 AND recipient_id>0 AND LENGTH(content_hash)=64),
 CHECK (due_at>=published_at AND (emitted_at IS NULL OR emitted_at>=due_at)),
 CHECK (emitted_at IS NULL OR cancelled_at IS NULL)
);
CREATE INDEX {{.Prefix}}idx_clientwork_acceptance_reminder_due ON {{.Prefix}}clientwork_acceptance_reminders(tenant_id,due_at,emitted_at,cancelled_at,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}acceptance_reminder_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}acceptance_reminder_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}clientwork_acceptance_reminders;
DROP TABLE {{.Prefix}}acceptance_reminder_rollback_guard;
DROP TABLE {{.Prefix}}clientwork_acceptance_reminders;

DROP INDEX {{.Prefix}}uq_clientwork_acceptance_pkg;
