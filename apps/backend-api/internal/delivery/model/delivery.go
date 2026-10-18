package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// Delivery is the persisted aggregate row defined by the delivery specifications.
// References to future same-module aggregates are bound by later migrations.
type Delivery struct {
	TenantID                guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                      guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID               guid.ID    `gorm:"column:project_id;not null"`
	SequenceNo              int        `gorm:"column:sequence_no;not null"`
	Number                  string     `gorm:"column:number;not null"`
	Title                   string     `gorm:"column:title;not null"`
	Summary                 *string    `gorm:"column:summary"`
	DemoURL                 *string    `gorm:"column:demo_url"`
	State                   string     `gorm:"column:state;not null"`
	ReleaseNotesMD          *string    `gorm:"column:release_notes_md"`
	ReleaseNotesSHA256      *string    `gorm:"column:release_notes_sha256"`
	ReleaseNotesActorKind   *string    `gorm:"column:release_notes_actor_kind"`
	ReleaseNotesActorID     *guid.ID   `gorm:"column:release_notes_actor_id"`
	ReleaseNotesDraftID     *guid.ID   `gorm:"column:release_notes_draft_id"`
	BundleState             string     `gorm:"column:bundle_state;not null"`
	BundleFingerprint       *string    `gorm:"column:bundle_fingerprint"`
	BundleFileID            *guid.ID   `gorm:"column:bundle_file_id"`
	ManifestFileID          *guid.ID   `gorm:"column:manifest_file_id"`
	BundleSHA256            *string    `gorm:"column:bundle_sha256"`
	ManifestSHA256          *string    `gorm:"column:manifest_sha256"`
	BundleSizeBytes         *int64     `gorm:"column:bundle_size_bytes"`
	BundleBuiltAt           *time.Time `gorm:"column:bundle_built_at"`
	BundleErrorCode         *string    `gorm:"column:bundle_error_code"`
	SendChannelKind         *string    `gorm:"column:send_channel_kind"`
	SendChannelID           *guid.ID   `gorm:"column:send_channel_id"`
	SendContactIDs          string     `gorm:"column:send_contact_ids;not null"`
	DeliverDecisionID       *guid.ID   `gorm:"column:deliver_decision_id"`
	DecidedAt               *time.Time `gorm:"column:decided_at"`
	SendAttempts            int        `gorm:"column:send_attempts;not null"`
	SendFailed              bool       `gorm:"column:send_failed;not null"`
	SentAt                  *time.Time `gorm:"column:sent_at"`
	AcceptanceDueAt         *time.Time `gorm:"column:acceptance_due_at"`
	AcceptanceID            *guid.ID   `gorm:"column:acceptance_id"`
	ResultAt                *time.Time `gorm:"column:result_at"`
	ReworkOfDeliveryID      *guid.ID   `gorm:"column:rework_of_delivery_id"`
	SourceIntakeIDs         string     `gorm:"column:source_intake_ids;not null"`
	ConfirmationRequestedAt *time.Time `gorm:"column:confirmation_requested_at"`
	ConfirmationRequestedBy *guid.ID   `gorm:"column:confirmation_requested_by"`
	ArchivedAt              *time.Time `gorm:"column:archived_at"`
	CreatedAt               time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	CreatedBy               *guid.ID   `gorm:"column:created_by"`
	UpdatedAt               time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	UpdatedBy               *guid.ID   `gorm:"column:updated_by"`
	Version                 int        `gorm:"column:version;not null"`
}

func (Delivery) TableName() string { return "delivery_deliveries" }
