-- Distinguishes clients whose authorization requests may be consented to
-- implicitly from those that must collect explicit consent.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_oauth_clients
    ADD COLUMN consent_mode VARCHAR(16) COLLATE "C" NOT NULL DEFAULT 'implicit',
    ADD CONSTRAINT {{.Prefix}}chk_iam_oauth_clients_consent_mode
        CHECK (consent_mode IN ('implicit', 'explicit'));

-- +goose Down
ALTER TABLE {{.Prefix}}iam_oauth_clients
    DROP CONSTRAINT {{.Prefix}}chk_iam_oauth_clients_consent_mode,
    DROP COLUMN consent_mode;
