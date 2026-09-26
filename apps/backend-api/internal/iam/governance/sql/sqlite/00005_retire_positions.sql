-- +goose Up
UPDATE {{.Prefix}}iam_access_review_items SET grant_type = 'group' WHERE grant_type = 'position';

-- +goose Down
SELECT 1;
