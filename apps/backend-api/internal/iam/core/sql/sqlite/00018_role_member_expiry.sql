-- A role assignment may name the moment it stops granting. NULL is no end,
-- which is what every row written before this migration means. The decision
-- paths filter on it rather than a job deleting expired rows: the authorization
-- answer is already computed against the request's clock, and a row that
-- outlives its grant is what lets a listing say when it ended.
--
-- ADD COLUMN is enough going up. Coming down is not symmetric: SQLite's
-- DROP COLUMN re-prepares every trigger in the schema to see whether any uses
-- the column, and this module's principal-erasure triggers reference a table
-- identity owns. Rolling core back on its own -- which the migration suite
-- does -- would fail on a trigger that was always allowed to name a table that
-- is not there yet. So the way down rebuilds the table instead, under
-- legacy_alter_table so the rename does not rewrite anything that points at the
-- name being recreated. Mirrors internal/iam/federation/sql/sqlite/00008.
-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE {{.Prefix}}iam_role_members
    ADD COLUMN expires_at DATETIME NULL;

-- The assignment is looked up by who holds it, which the primary key
-- (tenant, role, principal) cannot serve. The shape mirrors the index
-- iam_role_bindings already carries for the same question.
CREATE INDEX {{.Prefix}}idx_iam_role_members_subject
    ON {{.Prefix}}iam_role_members (tenant_id, principal_id, expires_at);

-- +goose Down
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;
BEGIN IMMEDIATE;
DROP INDEX {{.Prefix}}idx_iam_role_members_subject;
ALTER TABLE {{.Prefix}}iam_role_members RENAME TO {{.Prefix}}iam_role_members_old;
CREATE TABLE {{.Prefix}}iam_role_members (
    tenant_id    BIGINT   NOT NULL,
    role_id      BIGINT   NOT NULL,
    principal_id BIGINT   NOT NULL,
    created_at   DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, role_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_role_members_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES {{.Prefix}}iam_roles (tenant_id, id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_role_members (tenant_id, role_id, principal_id, created_at)
SELECT tenant_id, role_id, principal_id, created_at FROM {{.Prefix}}iam_role_members_old;
DROP TABLE {{.Prefix}}iam_role_members_old;
COMMIT;
PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
