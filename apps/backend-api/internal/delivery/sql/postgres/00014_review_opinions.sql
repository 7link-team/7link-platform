-- +goose Up
CREATE TABLE {{.Prefix}}delivery_review_opinions (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 sheet_id BIGINT NOT NULL, reviewer_id BIGINT NOT NULL, supersedes_id BIGINT,
 document TEXT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}ck_delivery_review_opinion_link CHECK(supersedes_id IS NULL OR supersedes_id <> id)
);
CREATE INDEX {{.Prefix}}idx_delivery_review_opinion_sheet ON {{.Prefix}}delivery_review_opinions(tenant_id,project_id,sheet_id,id);
CREATE INDEX {{.Prefix}}idx_delivery_review_opinion_reviewer ON {{.Prefix}}delivery_review_opinions(tenant_id,sheet_id,reviewer_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_review_opinions;
