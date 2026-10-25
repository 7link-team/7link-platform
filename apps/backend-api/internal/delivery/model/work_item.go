package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// WorkItem is the persisted aggregate row defined by the delivery specifications.
// References to future same-module aggregates are bound by later migrations.
type WorkItem struct {
	ReopenedFromWorkItemID *guid.ID   `gorm:"column:reopened_from_work_item_id"`
	EscapedFromStage       *string    `gorm:"column:escaped_from_stage"`
	ScheduledStart         *time.Time `gorm:"column:scheduled_start"`
	ScheduledEnd           *time.Time `gorm:"column:scheduled_end"`
	ScheduleTimezone       string     `gorm:"column:schedule_timezone;not null;default:UTC"`
	TenantID               guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                     guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID              guid.ID    `gorm:"column:project_id;not null"`
	RequirementID          *guid.ID   `gorm:"column:requirement_id"`
	Number                 int        `gorm:"column:number;not null"`
	ParentID               *guid.ID   `gorm:"column:parent_id"`
	IsContainer            bool       `gorm:"column:is_container;not null"`
	Title                  string     `gorm:"column:title;not null"`
	Description            string     `gorm:"column:description;not null"`
	Type                   string     `gorm:"column:type;not null"`
	Risk                   string     `gorm:"column:risk;not null"`
	EstimateMinutes        *int       `gorm:"column:estimate_minutes"`
	State                  string     `gorm:"column:state;not null"`
	BlockReasons           string     `gorm:"column:block_reasons;not null"`
	BlockNote              *string    `gorm:"column:block_note"`
	PausedAt               *time.Time `gorm:"column:paused_at"`
	PauseReason            *string    `gorm:"column:pause_reason"`
	PauseDecisionID        *guid.ID   `gorm:"column:pause_decision_id"`
	RequireHumanDecision   bool       `gorm:"column:require_human_decision;not null"`
	RequiredRuntime        *string    `gorm:"column:required_runtime"`
	SkillTags              string     `gorm:"column:skill_tags;not null"`
	PreferredSkillTags     string     `gorm:"column:preferred_skill_tags;not null"`
	UncertaintyFlag        bool       `gorm:"column:uncertainty_flag;not null"`
	BaselineVersionNo      int        `gorm:"column:baseline_version_no;not null"`
	BaselineStale          bool       `gorm:"column:baseline_stale;not null"`
	BaselineStaleSince     *int       `gorm:"column:baseline_stale_since"`
	CurrentClaimID         *guid.ID   `gorm:"column:current_claim_id"`
	CurrentExecutionID     *guid.ID   `gorm:"column:current_execution_id"`
	QualityReworkCount     int        `gorm:"column:quality_rework_count;not null"`
	InfraRetryCount        int        `gorm:"column:infra_retry_count;not null"`
	NeedsJudgement         bool       `gorm:"column:needs_judgement;not null"`
	FirstClaimedAt         *time.Time `gorm:"column:first_claimed_at"`
	AcceptedAt             *time.Time `gorm:"column:accepted_at"`
	CancelledAt            *time.Time `gorm:"column:cancelled_at"` //nolint:misspell // Spec 02 freezes this storage column spelling.
	FirstPass              *bool      `gorm:"column:first_pass"`
	AcceptedDecisionID     *guid.ID   `gorm:"column:accepted_decision_id"`
	FirstAcceptedAt        *time.Time `gorm:"column:first_accepted_at"`
	SelfConfidence         *int       `gorm:"column:self_confidence"`
	DecompositionID        *guid.ID   `gorm:"column:decomposition_id"`
	TemplateNodeKey        *string    `gorm:"column:template_node_key"`
	TemplateStageKey       *string    `gorm:"column:template_stage_key"`
	StateChangedAt         time.Time  `gorm:"column:state_changed_at;not null"`
	ArchivedAt             *time.Time `gorm:"column:archived_at"`
	AccountablePrincipalID guid.ID    `gorm:"column:accountable_principal_id;not null"`
	AutonomyLevel          string     `gorm:"column:autonomy_level;not null"`
	Objective              *string    `gorm:"column:objective"`
	OutOfScopeConfirmedAt  *time.Time `gorm:"column:out_of_scope_confirmed_at"`
	OutOfScope             string     `gorm:"column:out_of_scope;not null"`
	ContextRefs            string     `gorm:"column:context_refs;not null"`
	VerificationPlan       string     `gorm:"column:verification_plan;not null"`
	Budget                 *string    `gorm:"column:budget"`
	ReadinessOK            bool       `gorm:"column:readiness_ok;not null"`
	ReadinessIssues        string     `gorm:"column:readiness_issues;not null"`
	ReadinessOverrideBy    *guid.ID   `gorm:"column:readiness_override_by"`
	PlanRequired           bool       `gorm:"column:plan_required;not null"`
	CurrentPlanID          *guid.ID   `gorm:"column:current_plan_id"`
	InternalQARequired     bool       `gorm:"column:internal_qa_required;not null"`
	InternalRoundCount     int        `gorm:"column:internal_round_count;not null"`
	EscalationReason       *string    `gorm:"column:escalation_reason"`
	AppetiteMinutes        *int       `gorm:"column:appetite_minutes"`
	Tolerance              string     `gorm:"column:tolerance;not null"`
	StoppedFromState       *string    `gorm:"column:stopped_from_state"`
	ActiveExceptionID      *guid.ID   `gorm:"column:active_exception_id"`
	EscapeCount            int        `gorm:"column:escape_count;not null"`
	Severity               *string    `gorm:"column:severity"`
	Priority               *string    `gorm:"column:priority"`
	Reproducibility        *string    `gorm:"column:reproducibility"`
	FoundVersion           *string    `gorm:"column:found_version"`
	FixedVersion           *string    `gorm:"column:fixed_version"`
	IntroducedStage        *string    `gorm:"column:introduced_stage"`
	IntroducedReason       *string    `gorm:"column:introduced_reason"`
	Module                 *string    `gorm:"column:module"`
	DefectDetails          *string    `gorm:"column:defect_details"`
	DefectStage            *string    `gorm:"column:defect_stage"`
	ActivitySeq            int64      `gorm:"column:activity_seq;not null"`
	CreatedAt              time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	CreatedBy              *guid.ID   `gorm:"column:created_by"`
	UpdatedAt              time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	UpdatedBy              *guid.ID   `gorm:"column:updated_by"`
	Version                int        `gorm:"column:version;not null"`
}

func (WorkItem) TableName() string { return "delivery_work_items" }

// WorkItemActivity is an immutable, ordered work-context receipt.
type WorkItemActivity struct {
	TenantID   guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	ProjectID  guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	WorkItemID guid.ID   `gorm:"column:work_item_id;not null" json:"work_item_id"`
	Seq        int64     `gorm:"column:seq;not null" json:"seq"`
	Kind       string    `gorm:"column:kind;not null" json:"kind"`
	ActorKind  string    `gorm:"column:actor_kind;not null" json:"actor_kind"`
	ActorID    guid.ID   `gorm:"column:actor_id;not null" json:"actor_id"`
	RefKind    string    `gorm:"column:ref_kind;not null" json:"ref_kind"`
	RefID      guid.ID   `gorm:"column:ref_id;not null" json:"ref_id"`
	Title      string    `gorm:"column:title;not null" json:"title"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null;autoCreateTime:false" json:"occurred_at"`
}

func (WorkItemActivity) TableName() string { return "delivery_work_item_activities" }
