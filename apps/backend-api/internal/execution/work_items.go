package execution

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"gorm.io/gorm"
)

// WorkItems is a consumer-owned delivery port. Every call runs in execution's
// transaction and must lock/revalidate current project, work item and assignment.
// Caller locks actor, initiator and machine principals in ascending order before
// any project locks. Adapter must revalidate the exact assignment and initiator;
// it must not acquire additional principal locks after business locks.
// A missing adapter is an explicit error, never a synthetic assignment/Claim.
type WorkItems struct {
	ProgressStopOn          func(context.Context, *gorm.DB, WorkLink, string) error
	DistillationCompletedOn func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	SettleBudgetOn          func(context.Context, *gorm.DB, WorkLink, WorkResult) error
	AuthorizeVisualReviewOn func(context.Context, *gorm.DB, WorkLink, guid.ID, int, guid.ID, string) error
	ArtifactCriteriaOn      func(context.Context, *gorm.DB, WorkLink, []guid.ID) error
	AssignedAgentOn         func(context.Context, *gorm.DB, WorkLink) (guid.ID, error)
	PrincipalsOn            func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error)
	ReserveBudgetOn         func(context.Context, *gorm.DB, WorkLink, int64, bool) error
	BudgetOn                func(context.Context, *gorm.DB, WorkLink) (BudgetFact, error)
	AuthorizeOn             func(context.Context, *gorm.DB, WorkLink, string) error
	StartedOn               func(context.Context, *gorm.DB, WorkLink) error
	FinishedOn              func(context.Context, *gorm.DB, WorkLink, WorkResult) error
}
type WorkLink struct {
	ReviewPlanID                                                                     guid.ID
	Distillation                                                                     *DistillationPlan
	TenantID, ProjectID, WorkItemID, AssignmentID, ExecutionID, ActorID, InitiatorID guid.ID
	SourceHeadCommit, SourceFeedback                                                 string
	ParentExecutionID, SourceDecisionID                                              guid.ID
}
type WorkResult struct {
	ActualMicros              *int64
	Unused                    bool
	State, Reason, HeadCommit string
	Evidence                  []EvidenceFact
}
type ArtifactFact struct {
	BindingID   guid.ID  `json:"binding_id"`
	SpaceID     guid.ID  `json:"space_id"`
	FileID      guid.ID  `json:"file_id"`
	VersionID   guid.ID  `json:"version_id"`
	FileVersion int64    `json:"file_version" minimum:"1"`
	CriterionID *guid.ID `json:"criterion_id,omitempty"`
	Coverage    string   `json:"coverage" enum:"COVERED,PARTIAL,NOT_APPLICABLE"`
	Note        string   `json:"note" maxLength:"2000"`
	SHA256      string   `json:"sha256" minLength:"64" maxLength:"64"`
}
type EvidenceFact struct {
	Artifact    *ArtifactFact `json:"artifact,omitempty"`
	ID          guid.ID       `json:"id"`
	Kind        string        `json:"kind"`
	Title       string        `json:"title"`
	ContentHash string        `json:"content_hash"`
}

func (m *Module) workPrincipalLocks(ctx context.Context, tx *gorm.DB, l WorkLink, ids ...guid.ID) error {
	if m.work.PrincipalsOn != nil {
		extra, err := m.work.PrincipalsOn(ctx, tx, l)
		if err != nil {
			return err
		}
		ids = append(ids, extra...)
	}
	if l.InitiatorID == 0 && l.ReviewPlanID > 0 {
		filtered := ids[:0]
		for _, id := range ids {
			if id > 0 {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	} else if l.InitiatorID == 0 && l.ParentExecutionID > 0 && l.SourceDecisionID > 0 && l.ExecutionID > 0 {
		row, err := m.rowOn(ctx, tx, l.TenantID, l.ExecutionID, false)
		if err != nil {
			return err
		}
		if row.InitiatorID != 0 || row.BranchReason != "QUALITY" || row.ParentExecutionID == nil || *row.ParentExecutionID != l.ParentExecutionID || row.SourceDecisionID == nil || *row.SourceDecisionID != l.SourceDecisionID {
			return ErrForbidden
		}
		filtered := ids[:0]
		for _, id := range ids {
			if id != 0 {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	return m.principalLocks(ctx, tx, ids...)
}
