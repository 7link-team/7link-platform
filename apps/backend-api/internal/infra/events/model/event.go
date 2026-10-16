// Package model contains the persisted domain-event envelope.
package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// EventRow is an outbox entry. A nil StreamSeq means committed but not yet sequenced.
type EventRow struct {
	ID              guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID        guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID       *guid.ID   `gorm:"column:project_id"`
	EventType       string     `gorm:"column:event_type;size:96;not null"`
	SchemaVersion   int        `gorm:"column:schema_version;not null"`
	Durable         bool       `gorm:"column:durable;not null"`
	StreamSeq       *int64     `gorm:"column:stream_seq"`
	OccurredAt      time.Time  `gorm:"column:occurred_at;not null"`
	ActorKind       string     `gorm:"column:actor_kind;size:16;not null"`
	ActorID         *guid.ID   `gorm:"column:actor_id"`
	AuthoritySource string     `gorm:"column:authority_source;size:160;not null"`
	SubjectKind     string     `gorm:"column:subject_kind;size:64;not null"`
	SubjectID       guid.ID    `gorm:"column:subject_id;not null"`
	SubjectVersion  int64      `gorm:"column:subject_version;not null"`
	Topics          string     `gorm:"column:topics;not null"`
	RealtimeMode    string     `gorm:"column:realtime_mode;size:8;not null"`
	Data            string     `gorm:"column:data;not null"`
	CorrelationID   string     `gorm:"column:correlation_id;size:64;not null"`
	CausationID     *guid.ID   `gorm:"column:causation_id"`
	SequencedAt     *time.Time `gorm:"column:sequenced_at"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (EventRow) TableName() string { return "domain_events" }
