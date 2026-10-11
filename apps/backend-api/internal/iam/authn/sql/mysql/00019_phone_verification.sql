-- A phone number is proven by a code somebody reads off a message and types
-- back. It is a separate table from the email credential rather than a channel
-- column on it: an email credential is addressed by the link token in its
-- primary key, and a phone has no link, so folding one into the other would mean
-- inventing a primary key for something that has none. What the two flows
-- genuinely share -- the code generator, the attempt ceiling, the outbox, the
-- audit shape -- is shared in Go.
--
-- One outstanding code per person and purpose, which the primary key makes
-- true: asking again replaces that purpose's code rather than leaving two live.
-- The purpose is part of the key because a code is only valid for what it was
-- sent for: one sent to confirm a number must not sign anybody in, one sent to
-- sign in must not silently mark a number proven, and one sent as a second
-- factor must not do either. The number is kept with
-- the code so that changing the number invalidates it, and because it is PII the
-- erasure path deletes these rows with the rest.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_phone_verification_codes (
    principal_id BIGINT NOT NULL,
    purpose      VARCHAR(16) COLLATE utf8mb4_bin NOT NULL DEFAULT 'verify',
    phone        VARCHAR(32) COLLATE utf8mb4_bin NOT NULL,
    code         VARCHAR(6) COLLATE utf8mb4_bin NOT NULL,
    attempts     INT NOT NULL DEFAULT 0,
    created_at   DATETIME(3) NOT NULL,
    expires_at   DATETIME(3) NOT NULL,
    consumed_at  DATETIME(3) NULL,
    PRIMARY KEY (principal_id, purpose),
    CONSTRAINT {{.Prefix}}chk_iam_phone_verification_purpose
        CHECK (purpose IN ('verify', 'login', 'mfa')),
    CONSTRAINT {{.Prefix}}fk_authn_phone_verification_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_phone_verification_fields
        CHECK (attempts >= 0 AND OCTET_LENGTH(phone) >= 5 AND OCTET_LENGTH(phone) <= 32)
);

CREATE INDEX {{.Prefix}}idx_iam_phone_verification_active
    ON {{.Prefix}}iam_phone_verification_codes (phone, consumed_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_phone_verification_codes;
