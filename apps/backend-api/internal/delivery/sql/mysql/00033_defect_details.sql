-- +goose Up
ALTER TABLE {{.Prefix}}delivery_work_items ADD COLUMN defect_details LONGTEXT NULL;
ALTER TABLE {{.Prefix}}delivery_work_items ADD CONSTRAINT {{.Prefix}}chk_delivery_defect_details_json CHECK (defect_details IS NULL OR JSON_VALID(defect_details));

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_defect_details_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_defect_details_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_items WHERE defect_details IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_defect_details_rollback_guard;
ALTER TABLE {{.Prefix}}delivery_work_items DROP CHECK {{.Prefix}}chk_delivery_defect_details_json;
ALTER TABLE {{.Prefix}}delivery_work_items DROP COLUMN defect_details;
