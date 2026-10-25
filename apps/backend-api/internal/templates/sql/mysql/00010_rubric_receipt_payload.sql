-- +goose Up
-- Score receipts retain per-dimension notes, evidence and exact proof snapshots.
ALTER TABLE {{.Prefix}}templates_lifecycle_receipts MODIFY COLUMN payload MEDIUMTEXT NOT NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}templates_lifecycle_receipts MODIFY COLUMN payload TEXT NOT NULL;
