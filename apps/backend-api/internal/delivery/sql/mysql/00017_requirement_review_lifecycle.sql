-- +goose Up
ALTER TABLE {{.Prefix}}delivery_requirements ADD COLUMN review_state VARCHAR(32) NOT NULL DEFAULT 'NEEDS_WORK';
ALTER TABLE {{.Prefix}}delivery_requirements ADD CONSTRAINT {{.Prefix}}chk_delivery_req_review_state CHECK (review_state IN ('NEEDS_WORK','REVIEW_PENDING','APPROVED'));
ALTER TABLE {{.Prefix}}delivery_requirement_versions ADD COLUMN acceptance_suite TEXT NOT NULL DEFAULT ('{}');
ALTER TABLE {{.Prefix}}delivery_requirement_versions ADD COLUMN suite_hash VARCHAR(64) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE {{.Prefix}}delivery_requirement_versions DROP COLUMN suite_hash;
ALTER TABLE {{.Prefix}}delivery_requirement_versions DROP COLUMN acceptance_suite;
ALTER TABLE {{.Prefix}}delivery_requirements DROP CHECK {{.Prefix}}chk_delivery_req_review_state;
ALTER TABLE {{.Prefix}}delivery_requirements DROP COLUMN review_state;
