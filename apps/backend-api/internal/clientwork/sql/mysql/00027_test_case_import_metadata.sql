-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_test_cases ADD COLUMN requirement_id BIGINT;
ALTER TABLE {{.Prefix}}clientwork_test_cases ADD COLUMN criterion_id BIGINT;
ALTER TABLE {{.Prefix}}clientwork_test_cases ADD COLUMN criterion_source VARCHAR(2000) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}clientwork_test_cases ADD COLUMN preconditions VARCHAR(8000) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}clientwork_test_cases ADD COLUMN priority VARCHAR(2) NOT NULL DEFAULT 'P2';
ALTER TABLE {{.Prefix}}clientwork_test_cases ADD COLUMN unclassified BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE {{.Prefix}}clientwork_test_cases ADD CONSTRAINT {{.Prefix}}clientwork_test_case_priority_ck CHECK(priority IN ('P0','P1','P2','P3','P4','P5'));
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}test_case_import_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}test_case_import_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}clientwork_test_cases WHERE requirement_id IS NOT NULL OR criterion_id IS NOT NULL OR criterion_source<>'' OR preconditions<>'' OR priority<>'P2' OR unclassified=true;
DROP TABLE {{.Prefix}}test_case_import_rollback_guard;
ALTER TABLE {{.Prefix}}clientwork_test_cases DROP CHECK {{.Prefix}}clientwork_test_case_priority_ck;
ALTER TABLE {{.Prefix}}clientwork_test_cases DROP COLUMN unclassified;
ALTER TABLE {{.Prefix}}clientwork_test_cases DROP COLUMN priority;
ALTER TABLE {{.Prefix}}clientwork_test_cases DROP COLUMN preconditions;
ALTER TABLE {{.Prefix}}clientwork_test_cases DROP COLUMN criterion_source;
ALTER TABLE {{.Prefix}}clientwork_test_cases DROP COLUMN criterion_id;
ALTER TABLE {{.Prefix}}clientwork_test_cases DROP COLUMN requirement_id;
