-- +goose Up
CREATE TABLE {{.Prefix}}delivery_review_opinions (
 id INTEGER NOT NULL PRIMARY KEY, tenant_id INTEGER NOT NULL, project_id INTEGER NOT NULL,
 sheet_id INTEGER NOT NULL, reviewer_id INTEGER NOT NULL, supersedes_id INTEGER,
 document TEXT NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}ck_delivery_review_opinion_link CHECK(supersedes_id IS NULL OR supersedes_id <> id)
);
CREATE INDEX {{.Prefix}}idx_delivery_review_opinion_sheet ON {{.Prefix}}delivery_review_opinions(tenant_id,project_id,sheet_id,id);
CREATE INDEX {{.Prefix}}idx_delivery_review_opinion_reviewer ON {{.Prefix}}delivery_review_opinions(tenant_id,sheet_id,reviewer_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_review_opinions;
