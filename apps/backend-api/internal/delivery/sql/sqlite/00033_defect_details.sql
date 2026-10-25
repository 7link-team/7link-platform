-- +goose Up
ALTER TABLE {{.Prefix}}delivery_work_items ADD COLUMN defect_details TEXT NULL CONSTRAINT {{.Prefix}}chk_delivery_defect_details_json CHECK (defect_details IS NULL OR json_valid(defect_details));

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_defect_details_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_defect_details_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_items WHERE defect_details IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_defect_details_rollback_guard;
ALTER TABLE {{.Prefix}}delivery_work_items DROP COLUMN defect_details;
