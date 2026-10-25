-- +goose Up
ALTER TABLE {{.Prefix}}delivery_projects ADD COLUMN autonomy_version BIGINT NULL CHECK (autonomy_version>0);

-- +goose Down
-- A current autonomy episode cannot be discarded by downgrade.
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_autonomy_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_autonomy_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_projects WHERE autonomy_version IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_autonomy_rollback_guard;
ALTER TABLE {{.Prefix}}delivery_projects DROP COLUMN autonomy_version;
