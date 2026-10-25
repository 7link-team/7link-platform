package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Notification struct {
	ID                   guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID             guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID            guid.ID    `gorm:"column:project_id;not null"`
	RecipientPrincipalID guid.ID    `gorm:"column:recipient_principal_id;not null"`
	Type                 string     `gorm:"column:type;size:96;not null"`
	Severity             string     `gorm:"column:severity;size:8;not null"`
	TemplateKey          string     `gorm:"column:template_key;size:160;not null"`
	Params               string     `gorm:"column:params;not null"`
	LinkPath             string     `gorm:"column:link_path;size:512;not null"`
	Permission           string     `gorm:"column:permission;size:96;not null"`
	DedupeKey            *string    `gorm:"column:dedupe_key;size:160"`
	DedupeSlot           *string    `gorm:"column:dedupe_slot;size:160"`
	MergeCount           int        `gorm:"column:merge_count;not null"`
	ReadAt               *time.Time `gorm:"column:read_at"`
	ArchivedAt           *time.Time `gorm:"column:archived_at"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	LastMergedAt         time.Time  `gorm:"column:last_merged_at;not null"`
}

func (Notification) TableName() string { return "notifications" }

type Preference struct {
	TenantID      guid.ID `gorm:"column:tenant_id;primaryKey;autoIncrement:false" json:"tenant_id"`
	PrincipalID   guid.ID `gorm:"column:principal_id;primaryKey;autoIncrement:false" json:"-"`
	TypeKey       string  `gorm:"column:type_key;primaryKey;size:96" json:"type_key"`
	InApp         bool    `gorm:"column:in_app;not null" json:"in_app"`
	BrowserPush   bool    `gorm:"column:browser_push;not null" json:"browser_push"`
	Email         string  `gorm:"column:email;size:16;not null" json:"email" enum:"OFF,IMMEDIATE,DAILY_DIGEST"`
	QuietEnabled  bool    `gorm:"column:quiet_enabled;not null" json:"quiet_enabled"`
	QuietStartMin int     `gorm:"column:quiet_start_min;not null" json:"quiet_start_min" minimum:"0" maximum:"1439"`
	QuietEndMin   int     `gorm:"column:quiet_end_min;not null" json:"quiet_end_min" minimum:"0" maximum:"1439"`
	Timezone      string  `gorm:"column:timezone;size:64;not null" json:"timezone"`
	DigestHour    int     `gorm:"column:digest_hour;not null" json:"digest_hour" minimum:"0" maximum:"23"`
	Version       int64   `gorm:"column:version;not null" json:"version"`
}

func (Preference) TableName() string { return "notification_preferences" }

// RotationAlert is a permanent per-condition receipt, independent of unread
// merging and event receipt retention. One rotation has at most GRACE and FAILED.
type RotationAlert struct {
	TenantID   guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	RotationID guid.ID   `gorm:"column:rotation_id;primaryKey;autoIncrement:false;not null"`
	Condition  string    `gorm:"column:condition_key;primaryKey;size:8;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (RotationAlert) TableName() string { return "notification_rotation_alerts" }

// AgentError identifies one uninterrupted ERROR entry independently of mutable
// runtime timestamps. A later entry replaces its token, invalidating old jobs.
type AgentError struct {
	TenantID     guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	AgentID      guid.ID   `gorm:"column:agent_id;primaryKey;autoIncrement:false;not null"`
	EntryID      guid.ID   `gorm:"column:entry_id;not null"`
	SessionEpoch int       `gorm:"column:session_epoch;not null"`
	EnteredAt    time.Time `gorm:"column:entered_at;not null;autoCreateTime:false"`
	Active       bool      `gorm:"column:active;not null"`
	Notified     bool      `gorm:"column:notified;not null"`
}

func (AgentError) TableName() string { return "notification_agent_errors" }

// DispatchReceipt permanently binds a source proposal and recipient to its
// notification group, independently of event replay and unread slot rollover.
type DispatchReceipt struct {
	TenantID             guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ProposalID           guid.ID   `gorm:"column:proposal_id;primaryKey;autoIncrement:false;not null"`
	RecipientPrincipalID guid.ID   `gorm:"column:recipient_principal_id;primaryKey;autoIncrement:false;not null"`
	ProjectID            guid.ID   `gorm:"column:project_id;not null"`
	NotificationID       guid.ID   `gorm:"column:notification_id;not null"`
	CreatedAt            time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (DispatchReceipt) TableName() string { return "notification_dispatch_receipts" }

// IntakeReceipt permanently binds a source intake and recipient to its
// notification group, independently of event replay and unread slot rollover.
type IntakeReceipt struct {
	TenantID             guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	IntakeID             guid.ID   `gorm:"column:intake_id;primaryKey;autoIncrement:false;not null"`
	RecipientPrincipalID guid.ID   `gorm:"column:recipient_principal_id;primaryKey;autoIncrement:false;not null"`
	ChannelID            guid.ID   `gorm:"column:channel_id;not null"`
	ProjectID            guid.ID   `gorm:"column:project_id;not null"`
	NotificationID       guid.ID   `gorm:"column:notification_id;not null"`
	CreatedAt            time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (IntakeReceipt) TableName() string { return "notification_intake_receipts" }

// SourceReceipt binds one condition occurrence to its recipient and group.
// Receipt identity survives read/archive/window rollover and event replay.
type SourceReceipt struct {
	TenantID             guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	Type                 string    `gorm:"column:type_key;primaryKey;size:96;not null"`
	OccurrenceID         guid.ID   `gorm:"column:occurrence_id;primaryKey;autoIncrement:false;not null"`
	RecipientPrincipalID guid.ID   `gorm:"column:recipient_principal_id;primaryKey;autoIncrement:false;not null"`
	SourceID             guid.ID   `gorm:"column:source_id;not null"`
	NotificationID       guid.ID   `gorm:"column:notification_id;not null"`
	CreatedAt            time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (SourceReceipt) TableName() string { return "notification_source_receipts" }
