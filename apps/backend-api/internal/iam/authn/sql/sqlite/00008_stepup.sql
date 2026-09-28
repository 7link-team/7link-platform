-- Source: internal/modules/iam/sql/sqlite/00023_stepup_challenges.sql:2-15
-- +goose Up
CREATE TABLE {{.Prefix}}iam_stepup_challenges (
    id_hash      TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT   NOT NULL,
    session_hash TEXT     COLLATE BINARY NOT NULL,
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,
    consumed_at  DATETIME NULL,
    attempts     INTEGER  NOT NULL DEFAULT 0
);

CREATE INDEX {{.Prefix}}idx_iam_stepup_challenges_principal
    ON {{.Prefix}}iam_stepup_challenges (principal_id, consumed_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_stepup_challenges;
