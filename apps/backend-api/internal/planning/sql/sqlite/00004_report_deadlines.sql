-- +goose Up
ALTER TABLE {{.Prefix}}planning_reports ADD COLUMN due_at DATETIME NULL;
ALTER TABLE {{.Prefix}}planning_reports ADD COLUMN overdue_notice_count INTEGER NOT NULL DEFAULT 0 CHECK (overdue_notice_count>=0 AND overdue_notice_count<=2);
ALTER TABLE {{.Prefix}}planning_reports ADD COLUMN submitted_late BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX {{.Prefix}}planning_reports_due ON {{.Prefix}}planning_reports(tenant_id,state,due_at,id);
-- +goose Down
DROP INDEX {{.Prefix}}planning_reports_due;
ALTER TABLE {{.Prefix}}planning_reports DROP COLUMN submitted_late;
ALTER TABLE {{.Prefix}}planning_reports DROP COLUMN overdue_notice_count;
ALTER TABLE {{.Prefix}}planning_reports DROP COLUMN due_at;
