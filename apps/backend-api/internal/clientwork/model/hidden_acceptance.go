package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// HiddenCaseArchive preserves the exact bytes hashed into a frozen suite.
// No public resource binding or generic export is registered for this table.
type HiddenCaseArchive struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;primaryKey;autoIncrement:false;not null"`
	ContentHash string    `gorm:"column:content_hash;primaryKey;size:64;not null"`
	CaseID      guid.ID   `gorm:"column:case_id;not null"`
	CaseVersion int       `gorm:"column:case_version;not null"`
	Content     []byte    `gorm:"column:content;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (HiddenCaseArchive) TableName() string { return "clientwork_hidden_case_archive" }

// HiddenVerificationReceipt contains no free-form command output or case text.
type HiddenVerificationReceipt struct {
	TenantID            guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                  guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID           guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID          guid.ID   `gorm:"column:work_item_id;not null"`
	ResultID            guid.ID   `gorm:"column:result_id;not null"`
	SuiteHash           string    `gorm:"column:suite_hash;size:64;not null"`
	HeadCommit          string    `gorm:"column:head_commit;size:64;not null"`
	VerifierPrincipalID guid.ID   `gorm:"column:verifier_principal_id;not null"`
	Model               string    `gorm:"column:model;size:200;not null"`
	Family              string    `gorm:"column:family;size:100;not null"`
	Verdict             string    `gorm:"column:verdict;size:32;not null"`
	Reason              string    `gorm:"column:reason;size:64;not null"`
	InputHash           string    `gorm:"column:input_hash;size:64;not null"`
	OutputHash          string    `gorm:"column:output_hash;size:64;not null"`
	ReceiptHash         string    `gorm:"column:receipt_hash;size:64;not null"`
	CreatedAt           time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (HiddenVerificationReceipt) TableName() string { return "clientwork_hidden_verification_receipts" }
