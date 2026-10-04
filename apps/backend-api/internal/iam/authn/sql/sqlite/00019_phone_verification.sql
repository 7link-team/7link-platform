-- A phone number is proven by a code somebody reads off a message and types
-- back. It is a separate table from the email credential rather than a channel
-- column on it: an email credential is addressed by the link token in its
-- primary key, and a phone has no link, so folding one into the other would mean
-- inventing a primary key for something that has none. What the two flows
-- genuinely share -- the code generator, the attempt ceiling, the outbox, the
-- audit shape -- is shared in Go.
--
-- One outstanding code per person, which the primary key makes true: asking
-- again replaces the code rather than leaving two live. The number is kept with
-- the code so that changing the number invalidates it, and because it is PII the
-- erasure path deletes these rows with the rest.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_phone_verification_codes (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    phone        TEXT COLLATE BINARY NOT NULL,
    code         TEXT COLLATE BINARY NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,
    consumed_at  DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_authn_phone_verification_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_phone_verification_fields
        CHECK (attempts >= 0 AND length(CAST(phone AS BLOB)) >= 5 AND length(CAST(phone AS BLOB)) <= 32)
);

CREATE INDEX {{.Prefix}}idx_iam_phone_verification_active
    ON {{.Prefix}}iam_phone_verification_codes (phone, consumed_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_phone_verification_codes;
