-- +goose Up
CREATE TABLE {{.Prefix}}fleet_manual_model_catalogs (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 machine_id BIGINT NOT NULL,
 runtime VARCHAR(32) NOT NULL CHECK (runtime IN ('CODEX','CLAUDE_CODE')),
 models_json TEXT NOT NULL,
 families_json TEXT NOT NULL,
 version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
 updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}fleet_manual_models_machine FOREIGN KEY (tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id),
 CONSTRAINT {{.Prefix}}fleet_manual_models_runtime UNIQUE (tenant_id,machine_id,runtime)
);
-- +goose Down
DROP TABLE {{.Prefix}}fleet_manual_model_catalogs;
