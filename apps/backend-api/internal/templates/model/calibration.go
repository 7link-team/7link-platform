package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// CalibrationSample is an immutable Human label over the retained input bytes.
type CalibrationSample struct {
	TenantID          guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TemplateVersionID guid.ID   `gorm:"column:template_version_id;not null"`
	VerifierKey       string    `gorm:"column:verifier_key;not null"`
	Origin            string    `gorm:"column:origin;not null"`
	Input             string    `gorm:"column:input;not null"`
	Expected          string    `gorm:"column:expected;not null"`
	SHA256            string    `gorm:"column:sha256;not null"`
	CreatedBy         guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (CalibrationSample) TableName() string { return "templates_calibration_samples" }

type Calibration struct {
	TenantID          guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TemplateVersionID guid.ID    `gorm:"column:template_version_id;not null"`
	TemplateKey       string     `gorm:"column:template_key;not null"`
	VerifierKey       string     `gorm:"column:verifier_key;not null"`
	ProviderRef       string     `gorm:"column:provider_ref;not null"`
	Method            string     `gorm:"column:method;not null"`
	MachineID         guid.ID    `gorm:"column:machine_id;not null"`
	State             string     `gorm:"column:state;not null"`
	CurrentRunID      guid.ID    `gorm:"column:current_run_id;not null"`
	TargetPPM         int        `gorm:"column:target_ppm;not null"`
	CalibratedAt      *time.Time `gorm:"column:calibrated_at"`
	CalibratedBy      *guid.ID   `gorm:"column:calibrated_by"`
	DriftCheckedAt    *time.Time `gorm:"column:drift_checked_at"`
	NextReviewAt      time.Time  `gorm:"column:next_review_at;not null;autoCreateTime:false"`
	CreatedBy         guid.ID    `gorm:"column:created_by;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	Version           int        `gorm:"column:version;not null"`
}

func (Calibration) TableName() string { return "templates_verifier_calibrations" }

// CalibrationRun freezes input/label/version provenance before evaluation. A
// terminal run is never reused or rewritten by another calibration attempt.
type CalibrationRun struct {
	TenantID      guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	CalibrationID guid.ID    `gorm:"column:calibration_id;not null"`
	State         string     `gorm:"column:state;not null"`
	Snapshot      string     `gorm:"column:snapshot;not null"`
	SHA256        string     `gorm:"column:sha256;not null"`
	Results       string     `gorm:"column:results;not null"`
	AgreementPPM  *int       `gorm:"column:agreement_ppm"`
	SampleSize    int        `gorm:"column:sample_size;not null"`
	EscapeCount   int        `gorm:"column:escape_count;not null"`
	Recheck       bool       `gorm:"column:recheck;not null"`
	Limitations   string     `gorm:"column:limitations;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	CompletedAt   *time.Time `gorm:"column:completed_at"`
}

func (CalibrationRun) TableName() string { return "templates_calibration_runs" }
