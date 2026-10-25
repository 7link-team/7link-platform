-- +goose Up
ALTER TABLE {{.Prefix}}delivery_work_items MODIFY COLUMN requirement_id BIGINT NULL;
CREATE TABLE {{.Prefix}}delivery_work_item_criteria (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 position SMALLINT NOT NULL,
 text VARCHAR(2000) NOT NULL,
 verification_method VARCHAR(2000) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 created_by BIGINT NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_work_criterion_position UNIQUE (tenant_id,work_item_id,position),
 CONSTRAINT {{.Prefix}}fk_delivery_work_criterion_item FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (position BETWEEN 0 AND 49),
 CHECK (length(trim(text))>0),
 CHECK (length(trim(verification_method))>0)
);
CREATE TABLE {{.Prefix}}delivery_work_item_contracts (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 version_no INT NOT NULL,
 snapshot JSON NOT NULL,
 content_hash CHAR(64) NOT NULL,
 confirmed_at DATETIME(3) NOT NULL,
 confirmed_by BIGINT NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_work_contract_version UNIQUE (tenant_id,work_item_id,version_no),
 CONSTRAINT {{.Prefix}}fk_delivery_work_contract_item FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (version_no BETWEEN 1 AND 200),
 CHECK (length(content_hash)=64)
);

-- +goose Down
-- Refuse rollback when it would erase confirmed contracts or independent work.
-- IF NOT EXISTS permits retry after MySQL implicitly commits this DDL.
-- The guard contains no business records and is dropped on successful rollback.
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_work_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_work_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_item_contracts;
INSERT INTO {{.Prefix}}delivery_work_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_items WHERE requirement_id IS NULL;
DROP TABLE {{.Prefix}}delivery_work_rollback_guard;
DROP TABLE {{.Prefix}}delivery_work_item_contracts;
DROP TABLE {{.Prefix}}delivery_work_item_criteria;
-- MySQL rejects tightening nullability under a live FK and also rejects
-- dropping/reusing the same constraint name in a single ALTER.
ALTER TABLE {{.Prefix}}delivery_work_items DROP FOREIGN KEY {{.Prefix}}fk_delivery_wi_req;
ALTER TABLE {{.Prefix}}delivery_work_items
 MODIFY COLUMN requirement_id BIGINT NOT NULL,
 ADD CONSTRAINT {{.Prefix}}fk_delivery_wi_req FOREIGN KEY (tenant_id,project_id,requirement_id) REFERENCES {{.Prefix}}delivery_requirements (tenant_id,project_id,id) ON DELETE RESTRICT;
