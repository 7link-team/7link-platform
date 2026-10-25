-- +goose Up
ALTER TABLE {{.Prefix}}delivery_worklogs ADD COLUMN timezone VARCHAR(64) NULL;
ALTER TABLE {{.Prefix}}delivery_worklogs ADD COLUMN confirmed_by BIGINT NULL;
ALTER TABLE {{.Prefix}}delivery_worklogs ADD COLUMN confirmed_at TIMESTAMPTZ(3) NULL;
CREATE UNIQUE INDEX {{.Prefix}}uq_worklog_scope ON {{.Prefix}}delivery_worklogs(tenant_id,project_id,id);
CREATE TABLE {{.Prefix}}delivery_worklog_revisions (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 worklog_id BIGINT NOT NULL, version INTEGER NOT NULL, snapshot TEXT NOT NULL,
 action VARCHAR(64) NOT NULL, reason VARCHAR(500) NOT NULL, actor_id BIGINT NULL,
 recorded_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_worklog_revision UNIQUE(tenant_id,worklog_id,version),
 CONSTRAINT {{.Prefix}}fk_worklog_revision_scope FOREIGN KEY(tenant_id,project_id,worklog_id) REFERENCES {{.Prefix}}delivery_worklogs(tenant_id,project_id,id),
 CONSTRAINT {{.Prefix}}ck_worklog_revision_version CHECK(version>0)
);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}worklog_confirmation_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}worklog_confirmation_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_worklogs WHERE timezone IS NOT NULL OR confirmed_by IS NOT NULL OR confirmed_at IS NOT NULL;
INSERT INTO {{.Prefix}}worklog_confirmation_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_worklog_revisions;
DROP TABLE {{.Prefix}}worklog_confirmation_rollback_guard;
DROP TABLE {{.Prefix}}delivery_worklog_revisions;
DROP INDEX {{.Prefix}}uq_worklog_scope;
ALTER TABLE {{.Prefix}}delivery_worklogs DROP COLUMN confirmed_at;
ALTER TABLE {{.Prefix}}delivery_worklogs DROP COLUMN confirmed_by;
ALTER TABLE {{.Prefix}}delivery_worklogs DROP COLUMN timezone;
