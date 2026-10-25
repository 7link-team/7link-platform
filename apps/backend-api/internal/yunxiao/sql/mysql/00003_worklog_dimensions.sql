-- +goose Up
ALTER TABLE {{.Prefix}}delivery_worklogs ADD COLUMN work_item_id BIGINT NULL;
ALTER TABLE {{.Prefix}}delivery_worklogs ADD CONSTRAINT {{.Prefix}}ck_worklog_work_item CHECK (work_item_id IS NULL OR work_item_id > 0);
CREATE INDEX {{.Prefix}}idx_worklog_work_period ON {{.Prefix}}delivery_worklogs(tenant_id,project_id,work_item_id,work_date,id);
-- +goose Down
DROP INDEX {{.Prefix}}idx_worklog_work_period ON {{.Prefix}}delivery_worklogs;
ALTER TABLE {{.Prefix}}delivery_worklogs DROP CHECK {{.Prefix}}ck_worklog_work_item;
ALTER TABLE {{.Prefix}}delivery_worklogs DROP COLUMN work_item_id;
