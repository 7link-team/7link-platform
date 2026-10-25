// Package model owns intake persistence records.
package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Record stores the immutable source and mutable processing state of a requirement intake.
type Record struct {
	ID                          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	RefNo                       int        `gorm:"column:ref_no;not null"`
	ChannelID                   guid.ID    `gorm:"column:channel_id;not null"`
	ChannelKind                 string     `gorm:"column:channel_kind;not null;size:32"`
	ContactID                   guid.ID    `gorm:"column:contact_id;not null"`
	Kind                        string     `gorm:"column:kind;not null;size:32"`
	Status                      string     `gorm:"column:status;not null;size:32"`
	Title                       string     `gorm:"column:title;not null;size:200"`
	RawContent                  *string    `gorm:"column:raw_content"`
	RawContentFormat            string     `gorm:"column:raw_content_format;not null;size:32"`
	RawContentHash              string     `gorm:"column:raw_content_hash;not null;size:64"`
	SourceMessageID             *string    `gorm:"column:source_message_id;size:998"`
	ExternalRef                 *string    `gorm:"column:external_ref;size:128"`
	DesiredBy                   *string    `gorm:"column:desired_by"`
	ReceivedAt                  time.Time  `gorm:"column:received_at;not null"`
	SubmittedByPrincipalID      *guid.ID   `gorm:"column:submitted_by_principal_id"`
	ReplyToken                  string     `gorm:"column:reply_token;not null;size:16"`
	LatestCardRevision          int        `gorm:"column:latest_card_revision;not null"`
	OpenRoundID                 *guid.ID   `gorm:"column:open_round_id"`
	ClientUnresponsive          bool       `gorm:"column:client_unresponsive;not null"`
	ProjectID                   *guid.ID   `gorm:"column:project_id"`
	RequirementID               *guid.ID   `gorm:"column:requirement_id"`
	ConversionMode              *string    `gorm:"column:conversion_mode;size:32"`
	MergedIntoIntakeID          *guid.ID   `gorm:"column:merged_into_intake_id"`
	DeclineReasonCode           *string    `gorm:"column:decline_reason_code;size:32"`
	DeclineReason               *string    `gorm:"column:decline_reason;size:2000"`
	ClientMessage               *string    `gorm:"column:client_message;size:2000"`
	ClientNotificationState     string     `gorm:"column:client_notification_state;not null;default:NOT_REQUESTED;size:32"`
	ClientNotificationMessageID *guid.ID   `gorm:"column:client_notification_message_id"`
	ResolvedAt                  *time.Time `gorm:"column:resolved_at"`
	ResolvedActorKind           *string    `gorm:"column:resolved_actor_kind;size:32"`
	ResolvedActorID             *guid.ID   `gorm:"column:resolved_actor_id"`
	ResolvedAuthoritySource     *string    `gorm:"column:resolved_authority_source;size:80"`
	OriginDeliveryID            *guid.ID   `gorm:"column:origin_delivery_id"`
	OriginAcceptanceID          *guid.ID   `gorm:"column:origin_acceptance_id"`
	LastClientActivityAt        *time.Time `gorm:"column:last_client_activity_at"`
	LastTeamActivityAt          *time.Time `gorm:"column:last_team_activity_at"`
	LastTriageRequestedAt       *time.Time `gorm:"column:last_triage_requested_at"`
	ChangeOfIntakeID            *guid.ID   `gorm:"column:change_of_intake_id"`
	TargetRequirementID         *guid.ID   `gorm:"column:target_requirement_id"`
	ClientApproval              *string    `gorm:"column:client_approval"`
	ResultVersionNo             *int       `gorm:"column:result_version_no"`
	Version                     int        `gorm:"column:version;not null"`
	TenantID                    guid.ID    `gorm:"column:tenant_id;not null"`
	CreatedAt                   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt                   time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy                   *guid.ID   `gorm:"column:created_by"`
	UpdatedBy                   *guid.ID   `gorm:"column:updated_by"`
}

func (Record) TableName() string { return "intake_records" }
