package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type LiveState struct {
	WakeMode         string     `gorm:"column:wake_mode;not null;default:CHANNEL"`
	WakeURL          string     `gorm:"column:wake_url;not null"`
	WakeSecretSealed string     `gorm:"column:wake_secret_sealed;not null"`
	WakeVersion      int        `gorm:"column:wake_version;not null;default:1"`
	WakeFailures     int        `gorm:"column:wake_failures;not null"`
	LastWakeSeq      int64      `gorm:"column:last_wake_seq;not null"`
	NextWakeAt       *time.Time `gorm:"column:next_wake_at"`
	AgentPrincipalID guid.ID    `gorm:"column:agent_principal_id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID    `gorm:"column:tenant_id;not null"`
	Head             int64      `gorm:"column:head;not null"`
	Ack              int64      `gorm:"column:ack;not null"`
	Activity         string     `gorm:"column:activity;not null"`
	SessionEpoch     int        `gorm:"column:session_epoch;not null"`
	WorkspaceEpoch   int        `gorm:"column:workspace_epoch;not null"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (LiveState) TableName() string { return "agents_live_states" }

type InboxEvent struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	AgentPrincipalID guid.ID   `gorm:"column:agent_principal_id;not null"`
	Seq              int64     `gorm:"column:seq;not null"`
	SourceEventID    string    `gorm:"column:source_event_id;not null"`
	Kind             string    `gorm:"column:kind;not null"`
	SubjectID        guid.ID   `gorm:"column:subject_id;not null"`
	StreamKey        string    `gorm:"column:stream_key;not null"`
	StreamSeq        int64     `gorm:"column:stream_seq;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (InboxEvent) TableName() string { return "agents_inbox_events" }

type Reset struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	AgentPrincipalID guid.ID   `gorm:"column:agent_principal_id;not null"`
	Kind             string    `gorm:"column:kind;not null"`
	State            string    `gorm:"column:state;not null"`
	Reason           string    `gorm:"column:reason;not null"`
	RequestedBy      guid.ID   `gorm:"column:requested_by;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (Reset) TableName() string { return "agents_resets" }

type Reminder struct {
	AnchorKind       string     `gorm:"column:anchor_kind;not null;default:THREAD"`
	WorkItemID       *guid.ID   `gorm:"column:work_item_id"`
	ScheduleKind     string     `gorm:"column:schedule_kind;not null;default:ONCE"`
	PeriodKind       string     `gorm:"column:period_kind;not null;default:''"`
	Timezone         string     `gorm:"column:timezone;not null;default:''"`
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID    `gorm:"column:tenant_id;not null"`
	AgentPrincipalID guid.ID    `gorm:"column:agent_principal_id;not null"`
	ThreadID         guid.ID    `gorm:"column:thread_id;not null"`
	ProjectID        *guid.ID   `gorm:"column:project_id"`
	Title            string     `gorm:"column:title;not null"`
	Note             string     `gorm:"column:note;not null"`
	IntervalMinutes  int        `gorm:"column:interval_minutes;not null"`
	NextFireAt       *time.Time `gorm:"column:next_fire_at"`
	FireCount        int        `gorm:"column:fire_count;not null"`
	MaxFires         int        `gorm:"column:max_fires;not null"`
	State            string     `gorm:"column:state;not null"`
	Checks           string     `gorm:"column:checks;not null"`
	BudgetMinor      int64      `gorm:"column:budget_minor;not null"`
	Escalation       string     `gorm:"column:escalation;not null"`
	OutputFormat     string     `gorm:"column:output_format;not null"`
	Version          int        `gorm:"column:version;not null"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (Reminder) TableName() string { return "agents_reminders" }

type ToolEntry struct {
	ID                     guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID               guid.ID    `gorm:"column:tenant_id;not null"`
	EntryKey               string     `gorm:"column:entry_key;not null"`
	Kind                   string     `gorm:"column:kind;not null"`
	Title                  string     `gorm:"column:title;not null"`
	Transport              string     `gorm:"column:transport;not null"`
	Endpoint               string     `gorm:"column:endpoint;not null"`
	ContentHash            string     `gorm:"column:content_hash;not null"`
	SkillBody              string     `gorm:"column:skill_body;not null"`
	State                  string     `gorm:"column:state;not null"`
	CredentialSealed       string     `gorm:"column:credential_sealed;not null"`
	CredentialLast4        string     `gorm:"column:credential_last4;not null"`
	CredentialRotatedAt    *time.Time `gorm:"column:credential_rotated_at"`
	UpstreamAuthFailures   int        `gorm:"column:upstream_auth_failures;not null"`
	UpstreamAuthNotifiedAt *time.Time `gorm:"column:upstream_auth_notified_at"`
	Version                int        `gorm:"column:version;not null"`
	CreatedAt              time.Time  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (ToolEntry) TableName() string { return "agents_tool_entries" }

type ToolGrant struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	ToolID           guid.ID   `gorm:"column:tool_id;not null"`
	AgentPrincipalID guid.ID   `gorm:"column:agent_principal_id;not null"`
	State            string    `gorm:"column:state;not null"`
	Version          int       `gorm:"column:version;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (ToolGrant) TableName() string { return "agents_tool_grants" }
