-- +goose Up
ALTER TABLE {{.Prefix}}delivery_result_versions ADD COLUMN verification_plan TEXT NOT NULL DEFAULT '[]';
ALTER TABLE {{.Prefix}}delivery_result_versions ADD COLUMN source_execution_id INTEGER NULL;
ALTER TABLE {{.Prefix}}delivery_result_versions ADD COLUMN head_commit VARCHAR(64) NOT NULL DEFAULT '';
CREATE TABLE {{.Prefix}}delivery_verification_runs (
 tenant_id INTEGER NOT NULL, id INTEGER NOT NULL, project_id INTEGER NOT NULL,
 work_item_id INTEGER NOT NULL, result_id INTEGER NOT NULL, criterion_id INTEGER NOT NULL,
 method VARCHAR(16) NOT NULL, stage VARCHAR(24) NOT NULL, round INT NOT NULL,
 state VARCHAR(16) NOT NULL, verdict VARCHAR(16) NOT NULL, reason VARCHAR(2000) NOT NULL,
 reviewer_id INTEGER NULL, reviewer_model VARCHAR(160) NOT NULL DEFAULT '', reviewer_family VARCHAR(32) NOT NULL DEFAULT '', family_fallback BOOLEAN NOT NULL DEFAULT false,
 actor_id INTEGER NOT NULL, task_id INTEGER NULL, check_id INTEGER NULL,
 receipt_hash VARCHAR(64) NOT NULL, created_at DATETIME NOT NULL, completed_at DATETIME NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}fk_verification_result FOREIGN KEY (tenant_id,project_id,work_item_id,result_id) REFERENCES {{.Prefix}}delivery_result_versions (tenant_id,project_id,work_item_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}uq_verification_task UNIQUE (tenant_id,task_id),
 CHECK (method IN ('TEST','COMMAND','AGENT_JUDGE','HUMAN','VISUAL')),
 CHECK (stage IN ('AUTOMATED','AGENT_TEST','AGENT_ACCEPTANCE','HUMAN')),
 CHECK (round>0),
 CHECK ((state='PENDING' AND verdict='' AND completed_at IS NULL) OR (state='DONE' AND verdict IN ('PASSED','FAILED','INCONCLUSIVE') AND completed_at IS NOT NULL)),
 CHECK ((method='HUMAN' AND stage='HUMAN' AND check_id IS NOT NULL) OR (method<>'HUMAN' AND check_id IS NULL))
);
CREATE INDEX {{.Prefix}}idx_verification_result ON {{.Prefix}}delivery_verification_runs (tenant_id,project_id,work_item_id,result_id,id);
CREATE TABLE {{.Prefix}}delivery_verification_overrides (
 tenant_id INTEGER NOT NULL, id INTEGER NOT NULL, project_id INTEGER NOT NULL,
 work_item_id INTEGER NOT NULL, result_id INTEGER NOT NULL, baseline_version_no INT NOT NULL,
 artifact_hash CHAR(64) NOT NULL, verdicts TEXT NOT NULL, reason VARCHAR(2000) NOT NULL,
 risk VARCHAR(2000) NOT NULL, evidence_ids TEXT NOT NULL, actor_id INTEGER NOT NULL,
 authority_source VARCHAR(96) NOT NULL, reauthentication_hash CHAR(64) NOT NULL, created_at DATETIME NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}fk_override_result FOREIGN KEY (tenant_id,project_id,work_item_id,result_id) REFERENCES {{.Prefix}}delivery_result_versions (tenant_id,project_id,work_item_id,id) ON DELETE RESTRICT,
 CHECK (length(artifact_hash)=64 AND length(reauthentication_hash)=64 AND length(trim(reason))>0 AND length(trim(risk))>0)
);
CREATE INDEX {{.Prefix}}idx_override_result ON {{.Prefix}}delivery_verification_overrides (tenant_id,project_id,work_item_id,result_id,id);
-- +goose Down
CREATE TEMPORARY TABLE {{.Prefix}}delivery_verification_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_verification_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_verification_runs;
INSERT INTO {{.Prefix}}delivery_verification_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_verification_overrides;
INSERT INTO {{.Prefix}}delivery_verification_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_result_versions WHERE source_execution_id IS NOT NULL OR head_commit<>'' OR verification_plan<>'[]';
DROP TABLE {{.Prefix}}delivery_verification_rollback_guard;
DROP TABLE {{.Prefix}}delivery_verification_overrides;
DROP TABLE {{.Prefix}}delivery_verification_runs;
ALTER TABLE {{.Prefix}}delivery_result_versions DROP COLUMN verification_plan;
ALTER TABLE {{.Prefix}}delivery_result_versions DROP COLUMN head_commit;
ALTER TABLE {{.Prefix}}delivery_result_versions DROP COLUMN source_execution_id;
