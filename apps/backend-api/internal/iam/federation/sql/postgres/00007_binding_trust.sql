-- +goose Up
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN binding_provenance VARCHAR(32) NOT NULL DEFAULT 'unknown';
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN security_fingerprint VARCHAR(64) COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN quarantined_at TIMESTAMPTZ(3) NULL;
ALTER TABLE {{.Prefix}}iam_identity_links ADD COLUMN recovery_evidence TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Trust and invalidation state must survive operational rollback.
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'binding trust downgrade is forbidden';
END $$;
-- +goose StatementEnd
