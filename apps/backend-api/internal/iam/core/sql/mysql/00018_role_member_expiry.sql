-- A role assignment may name the moment it stops granting. NULL is no end,
-- which is what every row written before this migration means. The decision
-- paths filter on it rather than a job deleting expired rows: the authorization
-- answer is already computed against the request's clock, and a row that
-- outlives its grant is what lets a listing say when it ended.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_role_members
    ADD COLUMN expires_at DATETIME(3) NULL;

-- The assignment is looked up by who holds it, which the primary key
-- (tenant, role, principal) cannot serve. The shape mirrors the index
-- iam_role_bindings already carries for the same question.
CREATE INDEX {{.Prefix}}idx_iam_role_members_subject
    ON {{.Prefix}}iam_role_members (tenant_id, principal_id, expires_at);

-- +goose Down
DROP INDEX {{.Prefix}}idx_iam_role_members_subject ON {{.Prefix}}iam_role_members;

ALTER TABLE {{.Prefix}}iam_role_members DROP COLUMN expires_at;
