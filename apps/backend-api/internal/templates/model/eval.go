package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// EvalCase retains only pinned file references or a source-owned escape snapshot.
type EvalCase struct {
	TenantID          guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TemplateVersionID guid.ID   `gorm:"column:template_version_id;not null"`
	TemplateKey       string    `gorm:"column:template_key;not null"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null"`
	Origin            string    `gorm:"column:origin;not null"`
	Kind              string    `gorm:"column:kind;not null"`
	Title             string    `gorm:"column:title;not null"`
	InputRef          string    `gorm:"column:input_ref;not null"`
	Expected          string    `gorm:"column:expected;not null"`
	SourceEscapeID    *guid.ID  `gorm:"column:source_escape_id"`
	SourceSnapshot    string    `gorm:"column:source_snapshot;not null"`
	Active            bool      `gorm:"column:active;not null"`
	Version           int       `gorm:"column:version;not null"`
	CreatedBy         guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (EvalCase) TableName() string { return "templates_eval_cases" }

// EvalRun freezes complete case revisions and target provenance. Terminal
// results cannot be overwritten by a subsequent attempt or a case edit.
type EvalRun struct {
	TenantID          guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TemplateVersionID guid.ID    `gorm:"column:template_version_id;not null"`
	TemplateKey       string     `gorm:"column:template_key;not null"`
	TargetKey         string     `gorm:"column:target_key;not null"`
	Snapshot          string     `gorm:"column:snapshot;not null"`
	SHA256            string     `gorm:"column:sha256;not null"`
	State             string     `gorm:"column:state;not null"`
	Results           string     `gorm:"column:results;not null"`
	CasesTotal        int        `gorm:"column:cases_total;not null"`
	Passed            int        `gorm:"column:passed;not null"`
	Failed            int        `gorm:"column:failed;not null"`
	Inconclusive      int        `gorm:"column:inconclusive;not null"`
	AgreementPPM      *int       `gorm:"column:agreement_ppm"`
	Limitations       string     `gorm:"column:limitations;not null"`
	CreatedBy         guid.ID    `gorm:"column:created_by;not null"`
	StartedAt         time.Time  `gorm:"column:started_at;not null;autoCreateTime:false"`
	FinishedAt        *time.Time `gorm:"column:finished_at"`
}

func (EvalRun) TableName() string { return "templates_eval_runs" }
