-- +goose Up
CREATE TABLE {{.Prefix}}iam_field_policies (
    tenant_id BIGINT NOT NULL CHECK (tenant_id > 0),
    resource TEXT COLLATE BINARY NOT NULL,
    field_name TEXT COLLATE BINARY NOT NULL,
    policy_json TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, resource, field_name)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_field_policies;
