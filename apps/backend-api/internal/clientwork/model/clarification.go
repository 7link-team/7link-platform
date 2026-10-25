package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ClarificationRound freezes the questions offered to one current client member.
// Questions and answers share the round's concurrency version; source bytes live
// only in Intake and are never copied into this client-facing conversation.
type ClarificationRound struct {
	ChannelID      *guid.ID   `gorm:"column:channel_id"`
	ChannelVersion *int       `gorm:"column:channel_version"`
	ReminderHours  *int       `gorm:"column:reminder_hours"`
	MaxReminders   *int       `gorm:"column:max_reminders"`
	DueAt          *time.Time `gorm:"column:due_at"`
	NextReminderAt *time.Time `gorm:"column:next_reminder_at"`
	RemindersSent  int        `gorm:"column:reminders_sent;not null;default:0"`
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID       guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID      guid.ID    `gorm:"column:project_id;not null"`
	IntakeID       guid.ID    `gorm:"column:intake_id;not null"`
	IntakeVersion  int        `gorm:"column:intake_version;not null"`
	RecipientID    guid.ID    `gorm:"column:recipient_id;not null"`
	State          string     `gorm:"column:state;size:32;not null"`
	Questions      string     `gorm:"column:questions;not null"`
	Version        int        `gorm:"column:version;not null"`
	CreatedBy      guid.ID    `gorm:"column:created_by;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	ClosedAt       *time.Time `gorm:"column:closed_at"`
	CloseReason    *string    `gorm:"column:close_reason;size:32"`
	CloseNote      *string    `gorm:"column:close_note;size:2000"`
}

func (ClarificationRound) TableName() string { return "clientwork_clarification_rounds" }
