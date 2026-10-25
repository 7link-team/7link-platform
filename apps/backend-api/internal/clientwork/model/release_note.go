package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ReleaseNoteDraft freezes request sources before an Agent writes any content.
// A Human adoption always records separately edited final text in the package.
type ReleaseNoteDraft struct {
	TenantID           guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false" json:"-"`
	ID                 guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	ProjectID          guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	PackageID          guid.ID   `gorm:"column:package_id;not null" json:"package_id"`
	SourceFingerprint  string    `gorm:"column:source_fingerprint;not null;size:64" json:"source_fingerprint"`
	SourceSnapshot     string    `gorm:"column:source_snapshot;not null" json:"source_snapshot"`
	RequestedBy        guid.ID   `gorm:"column:requested_by;not null" json:"requested_by"`
	DrafterPrincipalID guid.ID   `gorm:"column:drafter_principal_id;not null" json:"drafter_principal_id"`
	Note               string    `gorm:"column:note;not null" json:"note"`
	Content            *string   `gorm:"column:content" json:"content,omitempty"`
	State              string    `gorm:"column:state;not null;size:16" json:"state"`
	AdoptedBy          *guid.ID  `gorm:"column:adopted_by" json:"adopted_by,omitempty"`
	Version            int       `gorm:"column:version;not null" json:"version"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false" json:"updated_at"`
}

func (ReleaseNoteDraft) TableName() string { return "clientwork_release_note_drafts" }
