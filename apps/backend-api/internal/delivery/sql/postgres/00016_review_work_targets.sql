-- +goose Up
ALTER TABLE {{.Prefix}}delivery_review_sheets ADD COLUMN work_item_id BIGINT;
ALTER TABLE {{.Prefix}}delivery_review_sheets ADD COLUMN open_work_item_id BIGINT;
CREATE UNIQUE INDEX {{.Prefix}}uq_delivery_open_work_review ON {{.Prefix}}delivery_review_sheets(tenant_id,open_work_item_id);
-- +goose Down
DROP INDEX {{.Prefix}}uq_delivery_open_work_review;
ALTER TABLE {{.Prefix}}delivery_review_sheets DROP COLUMN open_work_item_id;
ALTER TABLE {{.Prefix}}delivery_review_sheets DROP COLUMN work_item_id;
