-- Source: internal/modules/iam/sql/postgres/00023_stepup_challenges.sql:2-14
-- +goose Up
CREATE TABLE {{.Prefix}}iam_stepup_challenges (
    id_hash      CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    principal_id BIGINT         NOT NULL,
    session_hash CHAR(64)       COLLATE "C" NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    expires_at   TIMESTAMPTZ(3) NOT NULL,
    consumed_at  TIMESTAMPTZ(3) NULL,
    attempts     INTEGER        NOT NULL DEFAULT 0
);

CREATE INDEX {{.Prefix}}idx_iam_stepup_challenges_principal
    ON {{.Prefix}}iam_stepup_challenges (principal_id, consumed_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_stepup_challenges;
