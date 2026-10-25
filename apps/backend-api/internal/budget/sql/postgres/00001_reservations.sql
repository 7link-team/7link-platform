-- +goose Up
CREATE TABLE {{.Prefix}}budget_reservations (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 source VARCHAR(16) NOT NULL, source_id BIGINT NOT NULL,
 reserved_micros BIGINT NOT NULL, price_known BOOLEAN NOT NULL,
 actual_micros BIGINT NULL, state VARCHAR(16) NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL, settled_at TIMESTAMPTZ(3) NULL,
 CONSTRAINT {{.Prefix}}uq_budget_source UNIQUE (tenant_id, source, source_id),
 CONSTRAINT {{.Prefix}}chk_budget_source CHECK (source IN ('EXECUTION','GATEWAY')),
 CONSTRAINT {{.Prefix}}chk_budget_amount CHECK (reserved_micros>=0 AND (actual_micros IS NULL OR actual_micros>=0) AND (price_known=true OR reserved_micros=0)),
 CONSTRAINT {{.Prefix}}chk_budget_state CHECK ((state='RESERVED' AND settled_at IS NULL AND actual_micros IS NULL) OR (state='SETTLED' AND settled_at IS NOT NULL AND actual_micros IS NOT NULL))
);
CREATE INDEX {{.Prefix}}idx_budget_project ON {{.Prefix}}budget_reservations (tenant_id, project_id, state);
-- +goose Down
DROP TABLE {{.Prefix}}budget_reservations;
