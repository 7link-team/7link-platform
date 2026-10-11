-- A login challenge records what already proved itself. A second factor sent to
-- the same place as the first is the same factor twice, and this column is what
-- lets that be refused where the code is asked for rather than discovered after
-- it has been sent.
--
-- Existing rows were all opened by a password, which is what the default says.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_mfa_challenges
    ADD COLUMN first_factor VARCHAR(16) COLLATE utf8mb4_bin NOT NULL DEFAULT 'pwd';

-- +goose Down
ALTER TABLE {{.Prefix}}iam_mfa_challenges DROP COLUMN first_factor;
