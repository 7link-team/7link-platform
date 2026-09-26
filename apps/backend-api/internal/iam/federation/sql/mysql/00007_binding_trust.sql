-- +goose Up
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN binding_provenance VARCHAR(32) NOT NULL DEFAULT 'unknown';
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN security_fingerprint VARCHAR(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN quarantined_at DATETIME(3) NULL;
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN recovery_evidence VARCHAR(4096) NOT NULL DEFAULT '';

-- +goose Down
-- Trust and invalidation state must survive operational rollback.
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'binding trust downgrade is forbidden';
