-- +goose Up
ALTER TABLE {{.Prefix}}execution_evidence ADD COLUMN claim_id BIGINT NULL;
ALTER TABLE {{.Prefix}}execution_evidence ADD COLUMN state VARCHAR(16) NOT NULL DEFAULT 'DRAFT';
ALTER TABLE {{.Prefix}}execution_evidence ADD COLUMN producer_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}execution_evidence ADD COLUMN producer_kind VARCHAR(16) NOT NULL DEFAULT 'SYSTEM';
ALTER TABLE {{.Prefix}}execution_evidence ADD COLUMN scope_text TEXT NULL;
ALTER TABLE {{.Prefix}}execution_evidence ADD COLUMN limitations TEXT NULL;
ALTER TABLE {{.Prefix}}execution_evidence ADD COLUMN observed_at DATETIME(3) NULL;
ALTER TABLE {{.Prefix}}execution_evidence ADD CONSTRAINT {{.Prefix}}execution_evidence_claim_check CHECK (state IN ('DRAFT','ATTACHED','WITHDRAWN') AND ((state = 'ATTACHED' AND claim_id IS NOT NULL AND claim_id > 0) OR (state <> 'ATTACHED' AND claim_id IS NULL)));
ALTER TABLE {{.Prefix}}execution_evidence ADD CONSTRAINT {{.Prefix}}execution_evidence_producer_check CHECK ((producer_kind = 'SYSTEM' AND producer_id = 0) OR (producer_kind IN ('HUMAN','AGENT','MACHINE') AND producer_id > 0));
-- The exact committed completion receipt proves FinishedOn succeeded in the same
-- transaction; ordinary completion creates the claim with execution_id as its ID.
UPDATE {{.Prefix}}execution_evidence v
 JOIN (SELECT e.tenant_id AS proof_tenant_id, e.id AS proof_execution_id
 FROM {{.Prefix}}execution_executions e
 JOIN {{.Prefix}}execution_receipts r ON r.tenant_id = e.tenant_id AND r.execution_id = e.id
 AND r.dispatch_epoch = e.dispatch_epoch AND r.seq = e.last_seq
 AND r.kind = 'COMPLETED' AND r.state = 'SUCCEEDED'
 WHERE e.state = 'SUCCEEDED' AND e.result_state = 'CONFIRMED'
 AND e.state_reason = 'CLAIM_SUBMITTED'
 AND e.branch_reason NOT IN ('ENSEMBLE', 'SKILL_DISTILLATION')) proof
 ON proof.proof_tenant_id = v.tenant_id AND proof.proof_execution_id = v.execution_id
 SET v.state = 'ATTACHED', v.claim_id = v.execution_id;
-- +goose Down
ALTER TABLE {{.Prefix}}execution_evidence DROP CHECK {{.Prefix}}execution_evidence_claim_check;
ALTER TABLE {{.Prefix}}execution_evidence DROP CHECK {{.Prefix}}execution_evidence_producer_check;
ALTER TABLE {{.Prefix}}execution_evidence DROP COLUMN observed_at;
ALTER TABLE {{.Prefix}}execution_evidence DROP COLUMN limitations;
ALTER TABLE {{.Prefix}}execution_evidence DROP COLUMN scope_text;
ALTER TABLE {{.Prefix}}execution_evidence DROP COLUMN state;
ALTER TABLE {{.Prefix}}execution_evidence DROP COLUMN claim_id;
ALTER TABLE {{.Prefix}}execution_evidence DROP COLUMN producer_kind;
ALTER TABLE {{.Prefix}}execution_evidence DROP COLUMN producer_id;
