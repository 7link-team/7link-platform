-- +goose Up
CREATE TABLE {{.Prefix}}iam_registration_policy (
    id INTEGER NOT NULL PRIMARY KEY,
    enabled BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    updated_by BIGINT NOT NULL
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_registration_policy;
