-- Supports reverse relationship traversal in validateRelationshipAddition.
-- +goose Up
CREATE INDEX {{.Prefix}}idx_iam_relationships_reverse
    ON {{.Prefix}}iam_relationships (tenant_id, resource_id, relation);

-- +goose Down
DROP INDEX {{.Prefix}}idx_iam_relationships_reverse
    ON {{.Prefix}}iam_relationships;
