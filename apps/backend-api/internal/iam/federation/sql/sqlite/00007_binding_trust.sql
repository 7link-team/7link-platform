-- +goose Up
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN binding_provenance VARCHAR(32) NOT NULL DEFAULT 'unknown';
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN security_fingerprint VARCHAR(64) COLLATE BINARY NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN quarantined_at DATETIME NULL;
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN recovery_evidence TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Trust and invalidation state must survive operational rollback.
CREATE TEMP TABLE binding_trust_downgrade_guard (value INTEGER);
-- +goose StatementBegin
CREATE TEMP TRIGGER binding_trust_downgrade_refusal
BEFORE INSERT ON binding_trust_downgrade_guard
BEGIN
    SELECT RAISE(ABORT, 'binding trust downgrade is forbidden');
END;
-- +goose StatementEnd
INSERT INTO binding_trust_downgrade_guard (value) VALUES (1);
