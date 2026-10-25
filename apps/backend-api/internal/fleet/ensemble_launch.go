package fleet

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// EnsembleLaunchBranch is the immutable proposal approved by a Human. IDs of
// assignments and executions are allocated by the source, never supplied by HTTP.
type EnsembleLaunchBranch struct {
	PrincipalID  guid.ID         `json:"principal_id"`
	MachineID    guid.ID         `json:"machine_id"`
	Plan         json.RawMessage `json:"plan"`
	AssignmentID guid.ID         `json:"assignment_id" readOnly:"true"`
	ExecutionID  guid.ID         `json:"execution_id" readOnly:"true"`
}
type EnsemblePrepare struct {
	WorkItemID guid.ID                `json:"work_item_id"`
	Reason     string                 `json:"reason" minLength:"10" maxLength:"2000"`
	Branches   []EnsembleLaunchBranch `json:"branches" minItems:"2" maxItems:"5"`
}
type EnsembleBranchCheck func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error

// Every adapter operates in the source command transaction. LockOn gathers all
// participating principals before project/work/plan locks, including on replay.
type EnsembleRuntime struct {
	AuthorizeOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch, bool) error
	LockOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error
	AssignOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error
	LaunchOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error
	CancelOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error
}

func (m *Module) bindEnsembleRuntime(r EnsembleRuntime) error {
	dst := &m.ensemble.Runtime
	if r.AuthorizeOn != nil {
		if dst.AuthorizeOn != nil {
			return ErrInvalidArgument
		}
		dst.AuthorizeOn = r.AuthorizeOn
	}
	if r.LockOn != nil {
		if dst.LockOn != nil {
			return ErrInvalidArgument
		}
		dst.LockOn = r.LockOn
	}
	if r.AssignOn != nil {
		if dst.AssignOn != nil {
			return ErrInvalidArgument
		}
		dst.AssignOn = r.AssignOn
	}
	if r.LaunchOn != nil {
		if dst.LaunchOn != nil {
			return ErrInvalidArgument
		}
		dst.LaunchOn = r.LaunchOn
	}
	if r.CancelOn != nil {
		if dst.CancelOn != nil {
			return ErrInvalidArgument
		}
		dst.CancelOn = r.CancelOn
	}
	return nil
}

func (m *Module) lockEnsembleSnapshotOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, r *model.EnsemblePlan) error {
	if r.BranchSnapshot == nil {
		return nil
	}
	branches, err := ensembleSnapshot(r)
	if err != nil {
		return err
	}
	if m.ensemble.Runtime.LockOn == nil {
		return ErrUnavailable
	}
	return m.ensemble.Runtime.LockOn(ctx, tx, tenant, project, r.WorkItemID, branches)
}
func ensembleSnapshot(r *model.EnsemblePlan) ([]EnsembleLaunchBranch, error) {
	var branches []EnsembleLaunchBranch
	if r.BranchSnapshot == nil || json.Unmarshal([]byte(*r.BranchSnapshot), &branches) != nil || len(branches) < 2 || len(branches) > 5 {
		return nil, ErrInvalidArgument
	}
	return branches, nil
}
func (m *Module) checkEnsembleBranchOn(ctx context.Context, tx *gorm.DB, tenant, project, work, plan, assignment, execution guid.ID) error {
	if fleetTransaction(tx, tenant, project) != nil || work <= 0 || assignment <= 0 || execution <= 0 {
		return ErrInvalidArgument
	}
	q := query.BindEnsemblePlans(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(work), q.ID.Eq(plan)).First()
	if e != nil {
		return e
	}
	if r.ApprovedBy == nil || r.ApprovedAt == nil || r.State == "ABANDONED" {
		return ErrMachineForbidden
	}
	branches, e := ensembleSnapshot(r)
	if e != nil {
		return e
	}
	for _, b := range branches {
		if b.AssignmentID == assignment && b.ExecutionID == execution {
			return nil
		}
	}
	return ErrMachineForbidden
}
func (m *Module) PrepareEnsembleOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in EnsemblePrepare) (EnsembleView, error) {
	var out EnsembleView
	if fleetTransaction(tx, tenant, project) != nil || in.WorkItemID <= 0 || len(in.Branches) < 2 || len(in.Branches) > 5 || !utf8.ValidString(in.Reason) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 {
		return out, ErrInvalidArgument
	}
	if m.ensemble.Runtime.LockOn == nil || m.ids == nil {
		return out, ErrUnavailable
	}
	seen := map[guid.ID]bool{}
	for i := range in.Branches {
		b := &in.Branches[i]
		if b.PrincipalID <= 0 || b.MachineID <= 0 || seen[b.PrincipalID] || !json.Valid(b.Plan) {
			return out, ErrInvalidArgument
		}
		seen[b.PrincipalID] = true
		aid, e := m.ids.Next()
		if e != nil {
			return out, e
		}
		eid, e := m.ids.Next()
		if e != nil {
			return out, e
		}
		if aid <= 0 || eid <= 0 {
			return out, ErrUnavailable
		}
		b.AssignmentID = guid.ID(aid)
		b.ExecutionID = guid.ID(eid)
	}
	if e := m.ensemble.Runtime.LockOn(ctx, tx, tenant, project, in.WorkItemID, in.Branches); e != nil {
		return out, e
	}
	if e := m.ensembleAuthority(ctx, tx, tenant, project, in.WorkItemID, "work_item_assign"); e != nil {
		return out, e
	}
	id, e := m.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	raw, e := json.Marshal(in.Branches)
	if e != nil {
		return out, e
	}
	snapshot := string(raw)
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &model.EnsemblePlan{ID: guid.ID(id), TenantID: tenant, ProjectID: project, WorkItemID: in.WorkItemID, BranchSnapshot: &snapshot, State: "DRAFT", Reason: in.Reason, ComparisonJSON: "[]", Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now, UpdatedAt: now}
	if e = query.BindEnsemblePlans(tx, m.names).WithContext(ctx).Create(r); e != nil {
		return out, e
	}
	if e = m.ensembleEvent(ctx, tx, r, "PREPARED"); e != nil {
		return out, e
	}
	return m.ensembleViewOn(ctx, tx, r)
}
func (m *Module) ensembleLaunchSeed(ctx context.Context, tx *gorm.DB, tenant, project, plan guid.ID) (*model.EnsemblePlan, []EnsembleLaunchBranch, error) {
	q := query.BindEnsemblePlans(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(plan)).First()
	if e != nil {
		return nil, nil, e
	}
	b, e := ensembleSnapshot(r)
	return r, b, e
}
func (m *Module) launchEnsembleOn(ctx context.Context, tx *gorm.DB, tenant, project, plan guid.ID, version int, cancel bool) (EnsembleView, error) {
	var out EnsembleView
	seed, branches, e := m.ensembleLaunchSeed(ctx, tx, tenant, project, plan)
	if e != nil {
		return out, e
	}
	rt := m.ensemble.Runtime
	if rt.LockOn == nil || rt.AssignOn == nil || rt.LaunchOn == nil || rt.CancelOn == nil || rt.AuthorizeOn == nil {
		return out, ErrUnavailable
	}
	if e = rt.LockOn(ctx, tx, tenant, project, seed.WorkItemID, branches); e != nil {
		return out, e
	}
	if cancel {
		e = rt.AuthorizeOn(ctx, tx, tenant, project, seed.WorkItemID, plan, branches, true)
	} else {
		e = m.ensembleAuthority(ctx, tx, tenant, project, seed.WorkItemID, "work_item_assign")
		if e == nil {
			e = rt.AuthorizeOn(ctx, tx, tenant, project, seed.WorkItemID, plan, branches, false)
		}
	}
	if e != nil {
		return out, e
	}
	r, e := m.ensembleRowOn(ctx, tx, tenant, project, plan)
	if e != nil {
		return out, e
	}
	if r.Version != version || r.ApprovedBy == nil || r.ApprovedAt == nil || r.State != "DRAFT" {
		return out, ErrVersionConflict
	}
	bq := query.BindEnsembleBranches(tx, m.names)
	count, e := bq.WithContext(ctx).Where(bq.TenantID.Eq(tenant), bq.ProjectID.Eq(project), bq.PlanID.Eq(plan)).Count()
	if e != nil {
		return out, e
	}
	action := "LAUNCHED"
	if cancel {
		if count != int64(len(branches)) {
			return out, ErrVersionConflict
		}
		if e = rt.CancelOn(ctx, tx, tenant, project, r.WorkItemID, plan, branches); e != nil {
			return out, e
		}
		// A stop request remains DRAFT until runner receipts make comparison valid.
		action = "CANCEL_REQUESTED"
	} else {
		if count != 0 {
			return out, ErrVersionConflict
		}
		if e = rt.AssignOn(ctx, tx, tenant, project, r.WorkItemID, plan, branches); e != nil {
			return out, e
		}
		for _, b := range branches {
			bid, e := m.ids.Next()
			if e != nil {
				return out, e
			}
			if bid <= 0 {
				return out, ErrUnavailable
			}
			if e = bq.WithContext(ctx).Create(&model.EnsembleBranch{ID: guid.ID(bid), TenantID: tenant, ProjectID: project, PlanID: plan, ExecutionID: b.ExecutionID}); e != nil {
				return out, e
			}
		}
		if e = rt.LaunchOn(ctx, tx, tenant, project, r.WorkItemID, plan, branches); e != nil {
			return out, e
		}
	}
	q := query.BindEnsemblePlans(tx, m.names)
	r.Version++
	r.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(plan), q.Version.Eq(version)).Updates(map[string]any{"version": r.Version, "updated_at": r.UpdatedAt})
	if e != nil {
		return out, e
	}
	if changed.RowsAffected != 1 {
		return out, ErrVersionConflict
	}
	if e = m.ensembleEvent(ctx, tx, r, action); e != nil {
		return out, e
	}
	return m.ensembleViewOn(ctx, tx, r)
}
