package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// EnsembleSources is bound by domain owners. It never bypasses ordinary launch guards.
type EnsembleSources struct {
	Runtime     EnsembleRuntime
	AuthorizeOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error
	ExecutionOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (EnsembleExecution, error)
}
type EnsembleConnector struct {
	BindRuntime   func(EnsembleRuntime) error
	CheckBranchOn EnsembleBranchCheck
	BindWork      func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error) error
	BindExecution func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (EnsembleExecution, error)) error
}
type EnsembleExecution struct {
	ID          guid.ID `json:"id"`
	WorkItemID  guid.ID `json:"work_item_id"`
	PrincipalID guid.ID `json:"principal_id"`
	State       string  `json:"state"`
	ResultState string  `json:"result_state"`
}
type EnsembleBranchView struct {
	ID          guid.ID    `json:"id"`
	ExecutionID guid.ID    `json:"execution_id"`
	ArchivedAt  *time.Time `json:"archived_at"`
}
type EnsembleView struct {
	ProposedBranches    []EnsembleLaunchBranch `json:"proposed_branches,omitempty"`
	ApprovalRequestedAt *time.Time             `json:"approval_requested_at"`
	ApprovedBy          *guid.ID               `json:"approved_by"`
	ApprovedAt          *time.Time             `json:"approved_at"`
	SelectedBy          *guid.ID               `json:"selected_by"`
	SelectedAt          *time.Time             `json:"selected_at"`

	ID                   guid.ID              `json:"id"`
	ProjectID            guid.ID              `json:"project_id"`
	WorkItemID           guid.ID              `json:"work_item_id"`
	State                string               `json:"state"`
	Reason               string               `json:"reason"`
	Version              int                  `json:"version"`
	SelectedBranchID     *guid.ID             `json:"selected_branch_id"`
	SynthesisExecutionID *guid.ID             `json:"synthesis_execution_id"`
	Branches             []EnsembleBranchView `json:"branches"`
	Comparison           []EnsembleExecution  `json:"comparison"`
}
type EnsembleCreate struct {
	WorkItemID   guid.ID   `json:"work_item_id"`
	ExecutionIDs []guid.ID `json:"execution_ids" minItems:"2" maxItems:"5"`
	Reason       string    `json:"reason" minLength:"10" maxLength:"2000"`
}
type EnsembleDecision struct {
	Action               string  `json:"action" enum:"REQUEST_APPROVAL,APPROVE,COMPARE,SELECT,SYNTHESIZE,ABANDON"`
	BranchID             guid.ID `json:"branch_id,omitempty"`
	SynthesisExecutionID guid.ID `json:"synthesis_execution_id,omitempty"`
	Reason               string  `json:"reason" minLength:"10" maxLength:"2000"`
	Version              int     `json:"version" minimum:"1"`
}

func (m *Module) provideEnsembles(s *module.Services) error {
	if err := m.provideEnsembleNotices(s); err != nil {
		return err
	}
	return module.Provide[EnsembleConnector](s, EnsembleConnector{
		CheckBranchOn: m.checkEnsembleBranchOn,
		BindRuntime:   func(r EnsembleRuntime) error { return m.bindEnsembleRuntime(r) },
		BindWork: func(f func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error) error {
			if f == nil || m.ensemble.AuthorizeOn != nil {
				return ErrInvalidArgument
			}
			m.ensemble.AuthorizeOn = f
			return nil
		},
		BindExecution: func(f func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (EnsembleExecution, error)) error {
			if f == nil || m.ensemble.ExecutionOn != nil {
				return ErrInvalidArgument
			}
			m.ensemble.ExecutionOn = f
			return nil
		},
	})
}
func (m *Module) ensembleAuthority(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, permission string) error {
	if fleetTransaction(tx, tenant, project) != nil || work <= 0 {
		return ErrInvalidArgument
	}
	if m == nil || m.ensemble.AuthorizeOn == nil || m.ensemble.ExecutionOn == nil || m.ids == nil {
		return ErrUnavailable
	}
	return m.ensemble.AuthorizeOn(ctx, tx, tenant, project, work, permission)
}
func (m *Module) ensembleViewOn(ctx context.Context, tx *gorm.DB, r *model.EnsemblePlan) (EnsembleView, error) {
	v := EnsembleView{ApprovalRequestedAt: r.ApprovalRequestedAt, ApprovedBy: r.ApprovedBy, ApprovedAt: r.ApprovedAt, SelectedBy: r.SelectedBy, SelectedAt: r.SelectedAt, ID: r.ID, ProjectID: r.ProjectID, WorkItemID: r.WorkItemID, State: r.State, Reason: r.Reason, Version: r.Version, SelectedBranchID: r.SelectedBranchID, SynthesisExecutionID: r.SynthesisExecutionID, Branches: []EnsembleBranchView{}, Comparison: []EnsembleExecution{}}
	if json.Unmarshal([]byte(r.ComparisonJSON), &v.Comparison) != nil {
		return v, ErrUnavailable
	}
	if r.BranchSnapshot != nil {
		if json.Unmarshal([]byte(*r.BranchSnapshot), &v.ProposedBranches) != nil {
			return v, ErrUnavailable
		}
	}
	q := query.BindEnsembleBranches(tx, m.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ProjectID.Eq(r.ProjectID), q.PlanID.Eq(r.ID)).Order(q.ID).Find()
	for _, b := range rows {
		v.Branches = append(v.Branches, EnsembleBranchView{b.ID, b.ExecutionID, b.ArchivedAt})
	}
	return v, e
}
func (m *Module) ensembleRowOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (*model.EnsemblePlan, error) {
	q := query.BindEnsemblePlans(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrMachineNotFound
	}
	return r, e
}
func (m *Module) ensembleEvent(ctx context.Context, tx *gorm.DB, r *model.EnsemblePlan, action string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	ev := audit.NewDomainEvent(ctx, r.TenantID, "fleet.ensemble_"+strings.ToLower(action), "ensemble_plan", r.ID)
	ev.Detail["project_id"] = r.ProjectID.String()
	ev.Detail["work_item_id"] = r.WorkItemID.String()
	ev.Detail["version"] = r.Version
	if e := m.audit(ctx, tx, ev); e != nil {
		return e
	}
	_, e := m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: r.ProjectID, Type: "ensemble.changed", Subject: events.Subject{Kind: "ensemble_plan", ID: r.ID}, SubjectVersion: int64(r.Version), Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Data: map[string]any{"action": action, "state": r.State, "work_item_id": r.WorkItemID.String()}})
	return e
}

// CreateEnsembleOn records real execution alternatives. It does not claim that
// historical or queued executions ran concurrently, or manufacture runner facts.
func (m *Module) CreateEnsembleOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in EnsembleCreate) (EnsembleView, error) {
	var out EnsembleView
	if len(in.ExecutionIDs) < 2 || len(in.ExecutionIDs) > 5 || !utf8.ValidString(in.Reason) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 {
		return out, ErrInvalidArgument
	}
	if e := m.ensembleAuthority(ctx, tx, tenant, project, in.WorkItemID, "work_item_assign"); e != nil {
		return out, e
	}
	seen := map[guid.ID]bool{}
	for _, id := range in.ExecutionIDs {
		if id <= 0 || seen[id] {
			return out, ErrInvalidArgument
		}
		seen[id] = true
		f, e := m.ensemble.ExecutionOn(ctx, tx, tenant, project, id)
		if e != nil {
			return out, e
		}
		if f.WorkItemID != in.WorkItemID {
			return out, ErrInvalidArgument
		}
	}
	id, e := m.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &model.EnsemblePlan{ID: guid.ID(id), TenantID: tenant, ProjectID: project, WorkItemID: in.WorkItemID, State: "DRAFT", Reason: in.Reason, ComparisonJSON: "[]", Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now, UpdatedAt: now}
	if e = query.BindEnsemblePlans(tx, m.names).WithContext(ctx).Create(r); e != nil {
		return out, e
	}
	for _, execution := range in.ExecutionIDs {
		bid, e := m.ids.Next()
		if e != nil {
			return out, e
		}
		if bid <= 0 {
			return out, ErrUnavailable
		}
		b := &model.EnsembleBranch{ID: guid.ID(bid), TenantID: tenant, ProjectID: project, PlanID: r.ID, ExecutionID: execution}
		if e = query.BindEnsembleBranches(tx, m.names).WithContext(ctx).Create(b); e != nil {
			return out, e
		}
	}
	if e = m.ensembleEvent(ctx, tx, r, "CREATED"); e != nil {
		return out, e
	}
	return m.ensembleViewOn(ctx, tx, r)
}
func (m *Module) ChangeEnsembleOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, in EnsembleDecision) (EnsembleView, error) {
	var out EnsembleView
	// Read immutable work identity before locking; the owner takes project/work
	// locks first, matching execution and normal work-item commands.
	q := query.BindEnsemblePlans(tx, m.names)
	seed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if e != nil {
		return out, e
	}
	if e = m.lockEnsembleSnapshotOn(ctx, tx, tenant, project, seed); e != nil {
		return out, e
	}
	if e = m.ensembleAuthority(ctx, tx, tenant, project, seed.WorkItemID, "work_item_assign"); e != nil {
		return out, e
	}
	r, e := m.ensembleRowOn(ctx, tx, tenant, project, id)
	if e != nil {
		return out, e
	}
	if in.Version != r.Version || slices.Contains([]string{"SELECTED", "SYNTHESIZED", "ABANDONED"}, r.State) {
		return out, ErrVersionConflict
	}
	if !utf8.ValidString(in.Reason) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 {
		return out, ErrInvalidArgument
	}
	v, e := m.ensembleViewOn(ctx, tx, r)
	if e != nil {
		return out, e
	}
	switch in.Action {
	case "REQUEST_APPROVAL", "APPROVE":
		if r.State != "DRAFT" || r.ApprovedBy != nil {
			return out, ErrVersionConflict
		}
		actor := authn.PrincipalIDFromContext(ctx)
		if m.principals.ActiveHumanByID == nil {
			return out, ErrUnavailable
		}
		human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
		if err != nil {
			return out, err
		}
		if !active || human.ID != actor {
			return out, ErrMachineForbidden
		}
		at := time.Now().UTC().Truncate(time.Millisecond)
		if in.Action == "REQUEST_APPROVAL" {
			if r.ApprovalRequestedAt != nil {
				return out, ErrVersionConflict
			}
			r.ApprovalRequestedAt = &at
		} else {
			if r.ApprovalRequestedAt == nil {
				return out, ErrVersionConflict
			}
			r.ApprovedBy, r.ApprovedAt = &actor, &at
		}
	case "COMPARE":
		if r.BranchSnapshot != nil && (r.ApprovedBy == nil || len(v.Branches) < 2) {
			return out, ErrVersionConflict
		}
		if r.ApprovalRequestedAt != nil && r.ApprovedBy == nil {
			return out, ErrVersionConflict
		}
		facts := []EnsembleExecution{}
		for _, b := range v.Branches {
			f, e := m.ensemble.ExecutionOn(ctx, tx, tenant, project, b.ExecutionID)
			if e != nil {
				return out, e
			}
			if f.WorkItemID != r.WorkItemID {
				return out, ErrInvalidArgument
			}
			if !slices.Contains([]string{"SUCCEEDED", "FAILED", "CANCELLED", "INTERRUPTED"}, f.State) { //nolint:misspell // Frozen execution state.
				return out, ErrVersionConflict
			}
			facts = append(facts, f)
		}
		raw, e := json.Marshal(facts)
		if e != nil {
			return out, e
		}
		r.ComparisonJSON = string(raw)
		r.State = "COMPARING"
	case "SELECT", "SYNTHESIZE":
		if r.State != "COMPARING" {
			return out, ErrVersionConflict
		}
		var chosen *EnsembleBranchView
		for i := range v.Branches {
			if v.Branches[i].ID == in.BranchID {
				chosen = &v.Branches[i]
			}
		}
		if chosen == nil {
			return out, ErrInvalidArgument
		}
		f, e := m.ensemble.ExecutionOn(ctx, tx, tenant, project, chosen.ExecutionID)
		if e != nil {
			return out, e
		}
		if f.State != "SUCCEEDED" || f.WorkItemID != r.WorkItemID {
			return out, ErrVersionConflict
		}
		actor := authn.PrincipalIDFromContext(ctx)
		if m.principals.ActiveHumanByID == nil {
			return out, ErrUnavailable
		}
		principal, human, err := m.principals.ActiveHumanByID(ctx, tx, actor)
		if err != nil {
			return out, err
		}
		if !human || principal.ID != actor {
			return out, ErrMachineForbidden
		}
		if r.BranchSnapshot != nil {
			// A runtime branch is not a historical comparison. Selection must
			// submit its actual pending Claim in this transaction before the plan
			// can claim SELECTED; missing source support is explicit.
			return out, ErrUnavailable
		}
		at := time.Now().UTC().Truncate(time.Millisecond)
		r.SelectedBy, r.SelectedAt = &actor, &at
		r.SelectedBranchID = &chosen.ID
		r.State = "SELECTED"
		if in.Action == "SYNTHESIZE" {
			if in.SynthesisExecutionID <= 0 || slices.ContainsFunc(v.Branches, func(b EnsembleBranchView) bool { return b.ExecutionID == in.SynthesisExecutionID }) {
				return out, ErrInvalidArgument
			}
			s, e := m.ensemble.ExecutionOn(ctx, tx, tenant, project, in.SynthesisExecutionID)
			if e != nil {
				return out, e
			}
			if s.WorkItemID != r.WorkItemID || s.State != "SUCCEEDED" {
				return out, ErrVersionConflict
			}
			r.SynthesisExecutionID = &in.SynthesisExecutionID
			r.State = "SYNTHESIZED"
		}
	case "ABANDON":
		if r.BranchSnapshot != nil && len(v.Branches) > 0 {
			// The explicit cancel command retains the branch authorization until
			// real runner acknowledgements. Abandon cannot invalidate that fence
			// while a dispatched branch still needs to report its outcome.
			for _, branch := range v.Branches {
				fact, err := m.ensemble.ExecutionOn(ctx, tx, tenant, project, branch.ExecutionID)
				if err != nil {
					return out, err
				}
				if !slices.Contains([]string{"SUCCEEDED", "FAILED", "CANCELLED"}, fact.State) { //nolint:misspell // Frozen execution state.
					return out, ErrVersionConflict
				}
			}
		}
		r.State = "ABANDONED"
	default:
		return out, ErrInvalidArgument
	}
	r.Version++
	r.Reason = in.Reason
	r.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Version.Eq(in.Version)).Updates(map[string]any{"approval_requested_at": r.ApprovalRequestedAt, "approved_by": r.ApprovedBy, "approved_at": r.ApprovedAt, "selected_by": r.SelectedBy, "selected_at": r.SelectedAt, "state": r.State, "reason": r.Reason, "version": r.Version, "comparison_json": r.ComparisonJSON, "selected_branch_id": r.SelectedBranchID, "synthesis_execution_id": r.SynthesisExecutionID, "updated_at": r.UpdatedAt})
	if e != nil {
		return out, e
	}
	if changed.RowsAffected != 1 {
		return out, ErrVersionConflict
	}
	if r.State == "SELECTED" || r.State == "SYNTHESIZED" || r.State == "ABANDONED" {
		b := query.BindEnsembleBranches(tx, m.names)
		base := b.WithContext(ctx).Where(b.TenantID.Eq(tenant), b.ProjectID.Eq(project), b.PlanID.Eq(id))
		if r.SelectedBranchID != nil {
			base = base.Where(b.ID.Neq(*r.SelectedBranchID))
		}
		if _, e = base.Update(b.ArchivedAt, r.UpdatedAt); e != nil {
			return out, e
		}
	}
	if e = m.ensembleEvent(ctx, tx, r, in.Action); e != nil {
		return out, e
	}
	return m.ensembleViewOn(ctx, tx, r)
}
