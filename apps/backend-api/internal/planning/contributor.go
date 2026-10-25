package planning

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type GoalContributor struct {
	KR          string  `json:"kr"`
	PrincipalID guid.ID `json:"principal_id"`
	Source      string  `json:"source" enum:"OWNER,CHILD_GOAL,ASSIGNED"`
	ChildGoalID guid.ID `json:"child_goal_id,omitempty"`
}

type GoalAlignment struct {
	GoalID            guid.ID           `json:"goal_id"`
	Version           int               `json:"version"`
	Coverage          *float64          `json:"coverage,omitempty" doc:"Weighted coverage over authorized active/at-risk/achieved children; absent for MEMBER goals."`
	Uncovered         []string          `json:"uncovered"`
	Contributors      []GoalContributor `json:"contributors"`
	Children          []PlanningRecord  `json:"children"`
	VisibilityLimited bool              `json:"visibility_limited"`
}

func liveContribution(state string) bool {
	return slices.Contains([]string{"ACTIVE", "AT_RISK", "ACHIEVED"}, state)
}

func (m *Module) assignedContributorOn(ctx context.Context, tx *gorm.DB, tenant, goal, principal guid.ID, kr string) (bool, error) {
	q := query.BindContributors(tx, m.names)
	n, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.GoalID.Eq(goal), q.PrincipalID.Eq(principal), q.KR.Eq(kr)).Count()
	return n > 0, e
}

// Current goal state is always re-read; cancellation removes derived authority
// immediately without an asynchronous contributor projection.
func (m *Module) contributesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Goal, g PlanningGoalInput, principal guid.ID, kr string) (bool, error) {
	if !liveContribution(r.State) {
		return false, nil
	}
	if r.PrincipalID == principal {
		return true, nil
	}
	if g.Level == "MEMBER" {
		return false, nil
	}
	assigned, e := m.assignedContributorOn(ctx, tx, tenant, r.ID, principal, kr)
	if e != nil || assigned {
		return assigned, e
	}
	q := query.BindGoals(tx, m.names)
	request := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal), q.State.In("ACTIVE", "AT_RISK", "ACHIEVED"))
	if r.ProjectID > 0 {
		request = request.Where(q.ProjectID.Eq(r.ProjectID))
	}
	children, e := request.Limit(1001).Find()
	if e != nil {
		return false, e
	}
	if len(children) > 1000 {
		return false, ErrUnavailable
	}
	for _, child := range children {
		var body PlanningGoalInput
		if e = json.Unmarshal([]byte(child.Content), &body); e != nil {
			return false, e
		}
		if body.ParentGoalID != nil && *body.ParentGoalID == r.ID && body.ParentKR == kr {
			return true, nil
		}
	}
	return false, nil
}

func (m *Module) changeContributorOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Goal, g PlanningGoalInput, in PlanningGoalChange) error {
	if g.Level == "MEMBER" || !liveContribution(r.State) || in.PrincipalID <= 0 {
		return ErrInvalid
	}
	found := false
	for _, kr := range g.KeyResults {
		found = found || kr.Key == in.Key
	}
	if !found {
		return ErrInvalid
	}
	if in.PrincipalID == r.PrincipalID {
		return ErrConflict
	}
	if m.principals.LockActiveRow == nil || m.members == nil {
		return ErrUnavailable
	}
	p, active, e := m.principals.LockActiveRow(ctx, tx, in.PrincipalID)
	if e != nil {
		return e
	}
	q := query.BindContributors(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.GoalID.Eq(r.ID), q.PrincipalID.Eq(in.PrincipalID), q.KR.Eq(in.Key))
	if in.Action == "REMOVE_CONTRIBUTOR" {
		info, e := base.Delete()
		if e != nil {
			return e
		}
		if info.RowsAffected != 1 {
			return ErrConflict
		}
		return nil
	}
	if !active || p.ID != in.PrincipalID {
		return ErrForbidden
	}
	active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, in.PrincipalID)
	if e != nil {
		return e
	}
	if !active {
		return ErrForbidden
	}
	if r.ProjectID > 0 {
		c, e := m.entityPolicy(ctx, tx, tenant, r.ProjectID, "goal_read", in.PrincipalID)
		if e != nil {
			return e
		}
		if !c.AllowsEntityRows(r.ProjectID) || !(c.AllowAll || slices.Contains(c.OwnerIDs, in.PrincipalID.String()) || slices.Contains(c.ResourceIDs, r.ProjectID.String())) {
			return ErrForbidden
		}
	}
	exists, e := m.contributesOn(ctx, tx, tenant, r, g, in.PrincipalID, in.Key)
	if e != nil {
		return e
	}
	if exists {
		return ErrConflict
	}
	if m.ids == nil {
		return ErrUnavailable
	}
	id, e := m.ids.Next()
	if e != nil {
		return e
	}
	if id <= 0 {
		return ErrUnavailable
	}
	return q.WithContext(ctx).Create(&model.Contributor{TenantID: tenant, ID: guid.ID(id), GoalID: r.ID, KR: in.Key, PrincipalID: in.PrincipalID, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: m.now()})
}

func (m *Module) goalAlignmentOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (GoalAlignment, error) {
	out := GoalAlignment{GoalID: id, Uncovered: []string{}, Contributors: []GoalContributor{}, Children: []PlanningRecord{}}
	r, e := m.readOn(ctx, tx, tenant, id, "goal")
	if e != nil {
		return out, e
	}
	out.Version = r.Version
	var g PlanningGoalInput
	if e = json.Unmarshal(r.Content, &g); e != nil {
		return out, e
	}
	if liveContribution(r.State) {
		for _, kr := range g.KeyResults {
			out.Contributors = append(out.Contributors, GoalContributor{KR: kr.Key, PrincipalID: r.PrincipalID, Source: "OWNER"})
		}
	}
	q := query.BindGoals(tx, m.names)
	request := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("ACTIVE", "AT_RISK", "ACHIEVED"))
	if r.ProjectID > 0 {
		request = request.Where(q.ProjectID.Eq(r.ProjectID))
	}
	rows, e := request.Order(q.ID).Limit(1001).Find()
	if e != nil {
		return out, e
	}
	if len(rows) > 1000 {
		return out, ErrUnavailable
	}
	covered := map[string]bool{}
	for _, row := range rows {
		var child PlanningGoalInput
		if e = json.Unmarshal([]byte(row.Content), &child); e != nil {
			return out, e
		}
		if child.ParentGoalID == nil || *child.ParentGoalID != id {
			continue
		}
		visible, e := m.readOn(ctx, tx, tenant, row.ID, "goal")
		if errors.Is(e, ErrForbidden) {
			out.VisibilityLimited = true
			continue
		}
		if e != nil {
			return out, e
		}
		covered[child.ParentKR] = true
		out.Children = append(out.Children, visible)
		if liveContribution(r.State) {
			out.Contributors = append(out.Contributors, GoalContributor{KR: child.ParentKR, PrincipalID: row.PrincipalID, Source: "CHILD_GOAL", ChildGoalID: row.ID})
		}
	}
	if liveContribution(r.State) {
		c := query.BindContributors(tx, m.names)
		assigned, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.GoalID.Eq(id)).Order(c.KR, c.PrincipalID).Limit(1001).Find()
		if e != nil {
			return out, e
		}
		if len(assigned) > 1000 {
			return out, ErrUnavailable
		}
		for _, a := range assigned {
			out.Contributors = append(out.Contributors, GoalContributor{KR: a.KR, PrincipalID: a.PrincipalID, Source: "ASSIGNED"})
		}
	}
	if g.Level != "MEMBER" {
		var all, hit float64
		for _, kr := range g.KeyResults {
			w := progressWeight(kr.Weight)
			all += w
			if covered[kr.Key] {
				hit += w
			} else {
				out.Uncovered = append(out.Uncovered, kr.Key)
			}
		}
		if all > 0 {
			coverage := hit / all
			out.Coverage = &coverage
		}
	}
	return out, nil
}

func (m *Module) registerGoalAlignmentHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "planning.goal.alignment", Method: http.MethodGet, Path: "/goals/{record_id}/alignment", Summary: "Read authorized goal alignment and contributors", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, func(ctx context.Context, in *PlanningRecordPath) (*respx.Body[GoalAlignment], error) {
		tenant, id, e := recordIDs(in)
		if e != nil {
			return nil, planningError(ctx, e)
		}
		db, e := m.database()
		if e != nil {
			return nil, planningError(ctx, e)
		}
		var out GoalAlignment
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.goalAlignmentOn(ctx, tx, tenant, id); return e })
		if e != nil {
			return nil, planningError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
}
