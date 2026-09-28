-- +goose Up
CREATE TABLE {{.Prefix}}iam_principal_lifecycles (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    version BIGINT NOT NULL CHECK (version >= 1),
    retain_until TIMESTAMPTZ(3) NOT NULL,
    legal_hold BOOLEAN NOT NULL DEFAULT FALSE,
    reference VARCHAR(200) COLLATE "C" NOT NULL,
    anonymized_at TIMESTAMPTZ(3) NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}fk_iam_principal_lifecycle_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_principal_lifecycles;
