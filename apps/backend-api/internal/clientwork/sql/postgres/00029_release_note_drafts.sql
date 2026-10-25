-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_release_note_drafts (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL, package_id BIGINT NOT NULL,
 source_fingerprint VARCHAR(64) NOT NULL, source_snapshot TEXT NOT NULL,
 requested_by BIGINT NOT NULL, drafter_principal_id BIGINT NOT NULL,
 note TEXT NOT NULL, content TEXT, state VARCHAR(16) NOT NULL CHECK(state IN ('REQUESTED','DRAFT','ADOPTED')),
 adopted_by BIGINT, version INT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 PRIMARY KEY(tenant_id,id), FOREIGN KEY(tenant_id,package_id) REFERENCES {{.Prefix}}clientwork_packages(tenant_id,id)
);
CREATE INDEX {{.Prefix}}release_notes_package ON {{.Prefix}}clientwork_release_note_drafts(tenant_id,project_id,package_id,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}release_notes_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}release_notes_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}clientwork_release_note_drafts;
DROP TABLE {{.Prefix}}release_notes_rollback_guard;
DROP TABLE {{.Prefix}}clientwork_release_note_drafts;
