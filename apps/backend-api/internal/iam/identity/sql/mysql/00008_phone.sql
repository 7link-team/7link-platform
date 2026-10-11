-- A principal may have a phone number. It mirrors the email column beside it:
-- indexed but not unique, because the number is a way to reach somebody rather
-- than a way to name them -- login_name is the identity. A shared number, or one
-- a carrier released and reissued, must not be able to block an account.
--
-- It is PII from the moment it exists. Anonymization clears it with the address,
-- and the tombstone trigger on this table already refuses every update to an
-- anonymized principal, so nothing can write it back afterwards.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_principals
    ADD COLUMN phone VARCHAR(32) COLLATE utf8mb4_bin NOT NULL DEFAULT '';

ALTER TABLE {{.Prefix}}iam_principals
    ADD COLUMN phone_verified TINYINT(1) NOT NULL DEFAULT 0;

CREATE INDEX {{.Prefix}}idx_iam_principals_phone
    ON {{.Prefix}}iam_principals (phone);

-- +goose Down
DROP INDEX {{.Prefix}}idx_iam_principals_phone ON {{.Prefix}}iam_principals;

ALTER TABLE {{.Prefix}}iam_principals DROP COLUMN phone_verified;

ALTER TABLE {{.Prefix}}iam_principals DROP COLUMN phone;
