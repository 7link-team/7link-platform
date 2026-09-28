-- Distinguishes clients whose authorization requests may be consented to
-- implicitly from those that must collect explicit consent.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_oauth_clients
    ADD COLUMN consent_mode TEXT COLLATE BINARY NOT NULL DEFAULT 'implicit'
        CHECK (consent_mode IN ('implicit', 'explicit'));

-- +goose Down
ALTER TABLE {{.Prefix}}iam_oauth_clients DROP COLUMN consent_mode;
