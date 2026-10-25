package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type TestCaseBody struct {
	RequirementID      *guid.ID `json:"requirement_id,omitempty"`
	CriterionID        *guid.ID `json:"criterion_id,omitempty"`
	CriterionSource    string   `json:"criterion_source,omitempty" maxLength:"2000"`
	Preconditions      string   `json:"preconditions,omitempty" maxLength:"8000"`
	Priority           string   `json:"priority,omitempty" enum:"P0,P1,P2,P3,P4,P5"`
	IsHiddenAcceptance bool     `json:"is_hidden_acceptance,omitempty" default:"false"`
	Title              string   `json:"title" minLength:"1" maxLength:"200"`
	Steps              string   `json:"steps" minLength:"1" maxLength:"8000"`
	Expected           string   `json:"expected" minLength:"1" maxLength:"2000"`
	WorkItemID         guid.ID  `json:"work_item_id"`
	DefectID           *guid.ID `json:"defect_id,omitempty"`
}
type TestCaseView struct {
	RequirementID      *guid.ID `json:"requirement_id,omitempty"`
	CriterionID        *guid.ID `json:"criterion_id,omitempty"`
	CriterionSource    string   `json:"criterion_source,omitempty"`
	Preconditions      string   `json:"preconditions,omitempty"`
	Priority           string   `json:"priority,omitempty"`
	Unclassified       bool     `json:"unclassified,omitempty"`
	IsHiddenAcceptance bool     `json:"is_hidden_acceptance"`
	ID                 guid.ID  `json:"id"`
	Version            int      `json:"version"`
	Title              string   `json:"title"`
	Steps              string   `json:"steps"`
	Expected           string   `json:"expected"`
	WorkItemID         guid.ID  `json:"work_item_id"`
	DefectID           *guid.ID `json:"defect_id,omitempty"`
}

func caseView(r *model.TestCase) TestCaseView {
	return TestCaseView{RequirementID: r.RequirementID, CriterionID: r.CriterionID, CriterionSource: r.CriterionSource, Preconditions: r.Preconditions, Priority: r.Priority, Unclassified: r.Unclassified, IsHiddenAcceptance: r.IsHiddenAcceptance, ID: r.ID, Version: r.Version, Title: r.Title, Steps: r.Steps, Expected: r.Expected, WorkItemID: r.WorkItemID, DefectID: r.DefectID}
}
func (m *Module) caseOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) (*model.TestCase, error) {
	q := query.BindTestCase(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).First()
}
func (m *Module) writeCaseOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in TestCaseBody) (Receipt, error) {
	if !validText(in.Title, 200) || !validText(in.Steps, 8000) || !validText(in.Expected, 2000) || in.WorkItemID <= 0 {
		return Receipt{}, ErrInvalid
	}
	if _, e := m.authorize(ctx, tx, p, "test_case_manage", false); e != nil {
		return Receipt{}, e
	}
	if in.IsHiddenAcceptance {
		if e := m.requireHiddenAcceptanceOn(ctx, tx, p); e != nil {
			return Receipt{}, e
		}
	}
	tenant := authn.TenantIDFromContext(ctx)
	if e := m.projects.ValidateWorkOn(ctx, tx, tenant, p, in.WorkItemID, false); e != nil {
		return Receipt{}, e
	}
	if in.DefectID != nil {
		if e := m.projects.ValidateWorkOn(ctx, tx, tenant, p, *in.DefectID, true); e != nil {
			return Receipt{}, e
		}
	}
	q := query.BindTestCase(tx, m.names)
	var row *model.TestCase
	var e error
	if id == 0 {
		id, e = m.nextID()
		if e != nil {
			return Receipt{}, e
		}
		row = &model.TestCase{TenantID: tenant, ID: id, ProjectID: p, Version: 1, ActorID: authn.PrincipalIDFromContext(ctx), CreatedAt: now()}
	} else {
		row, e = m.caseOn(ctx, tx, p, id)
		if e != nil {
			return Receipt{}, e
		}
		if row.IsHiddenAcceptance {
			if e := m.requireHiddenAcceptanceOn(ctx, tx, p); e != nil {
				return Receipt{}, e
			}
		}
		// Previously public versions may already be in shared immutable snapshots.
		if row.IsHiddenAcceptance != in.IsHiddenAcceptance {
			return Receipt{}, ErrConflict
		}
		if row.Version != v {
			return Receipt{}, ErrConflict
		}
		row.Version++
	}
	row.IsHiddenAcceptance = in.IsHiddenAcceptance
	// Older editors omit import metadata; retain it when editing an imported case.
	if v > 0 && row.RequirementID != nil && in.RequirementID == nil {
		in.RequirementID, in.CriterionID, in.CriterionSource, in.Preconditions, in.Priority = row.RequirementID, row.CriterionID, row.CriterionSource, row.Preconditions, row.Priority
	}
	if in.Priority == "" {
		in.Priority = "P2"
	}
	if err := m.validateTestCaseClassificationOn(ctx, tx, p, in); err != nil {
		return Receipt{}, err
	}
	row.RequirementID, row.CriterionID, row.CriterionSource, row.Preconditions, row.Priority = in.RequirementID, in.CriterionID, in.CriterionSource, in.Preconditions, in.Priority
	row.Unclassified = in.RequirementID != nil && in.CriterionID == nil
	row.Title = in.Title
	row.Steps = in.Steps
	row.Expected = in.Expected
	row.WorkItemID = in.WorkItemID
	row.DefectID = in.DefectID
	row.UpdatedAt = now()
	if row.IsHiddenAcceptance {
		// SQL errors and slow-query tracing must never interpolate hidden content.
		q = query.BindTestCase(tx.Session(&gorm.Session{Logger: logger.Discard}), m.names)
	}
	if v == 0 {
		e = q.WithContext(ctx).Create(row)
	} else {
		_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Select(q.Title, q.Steps, q.Expected, q.WorkItemID, q.DefectID, q.Version, q.UpdatedAt, q.RequirementID, q.CriterionID, q.CriterionSource, q.Preconditions, q.Priority, q.Unclassified).Updates(row)
	}
	if e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "test_case_written", "test_case_manage", row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: "ACTIVE"}, nil
}

type TestPlanBody struct {
	Title   string    `json:"title" minLength:"1" maxLength:"200"`
	CaseIDs []guid.ID `json:"case_ids" minItems:"1" maxItems:"100"`
}
type TestPlanView struct {
	ID      guid.ID        `json:"id"`
	Title   string         `json:"title"`
	State   string         `json:"state"`
	Version int            `json:"version"`
	Cases   []TestCaseView `json:"cases"`
}

func planView(r *model.TestPlan) (TestPlanView, error) {
	v := TestPlanView{ID: r.ID, Title: r.Title, State: r.State, Version: r.Version}
	e := json.Unmarshal([]byte(r.Cases), &v.Cases)
	return v, e
}
func (m *Module) planOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) (*model.TestPlan, error) {
	q := query.BindTestPlan(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).First()
}
func (m *Module) createPlanOn(ctx context.Context, tx *gorm.DB, p guid.ID, in TestPlanBody) (Receipt, error) {
	if !validText(in.Title, 200) || len(in.CaseIDs) < 1 || len(in.CaseIDs) > 100 {
		return Receipt{}, ErrInvalid
	}
	if _, e := m.authorize(ctx, tx, p, "test_plan_manage", false); e != nil {
		return Receipt{}, e
	}
	cases := []TestCaseView{}
	seen := map[guid.ID]bool{}
	for _, id := range in.CaseIDs {
		if id <= 0 || seen[id] {
			return Receipt{}, ErrInvalid
		}
		seen[id] = true
		r, e := m.caseOn(ctx, tx, p, id)
		if e != nil {
			return Receipt{}, e
		}
		// Manual plans are shared and must never copy hidden case content.
		if r.IsHiddenAcceptance {
			return Receipt{}, ErrNotFound
		}
		cases = append(cases, caseView(r))
	}
	data, _ := json.Marshal(cases)
	if len(data) > 500000 {
		return Receipt{}, ErrInvalid
	}
	id, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	row := &model.TestPlan{TenantID: authn.TenantIDFromContext(ctx), ID: id, ProjectID: p, Title: in.Title, Cases: string(data), State: "FROZEN", Version: 1, ActorID: authn.PrincipalIDFromContext(ctx), CreatedAt: now(), UpdatedAt: now()}
	if e = query.BindTestPlan(tx, m.names).WithContext(ctx).Create(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "test_plan_created", "test_plan_manage", 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: 1, State: row.State}, nil
}

type TestRunBody struct {
	Defect      *delivery.TestFailureDefect `json:"defect,omitempty"`
	CaseID      guid.ID                     `json:"case_id"`
	CaseVersion int                         `json:"case_version" minimum:"1"`
	Result      string                      `json:"result" enum:"PASSED,FAILED,BLOCKED"`
	Evidence    string                      `json:"evidence" minLength:"1" maxLength:"8000"`
	DefectID    *guid.ID                    `json:"defect_id,omitempty"`
}
type TestRunView struct {
	ID          guid.ID  `json:"id"`
	PlanID      guid.ID  `json:"plan_id"`
	CaseID      guid.ID  `json:"case_id"`
	CaseVersion int      `json:"case_version"`
	Result      string   `json:"result"`
	Evidence    string   `json:"evidence"`
	DefectID    *guid.ID `json:"defect_id,omitempty"`
	Source      string   `json:"source" enum:"HUMAN_RECORDED"`
}

func runView(r *model.TestRun) TestRunView {
	return TestRunView{r.ID, r.PlanID, r.CaseID, r.CaseVersion, r.Result, r.Evidence, r.DefectID, "HUMAN_RECORDED"}
}
func (m *Module) recordRunOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in TestRunBody) (Receipt, error) {
	if !validText(in.Evidence, 8000) || (in.Result != "PASSED" && in.Result != "FAILED" && in.Result != "BLOCKED") {
		return Receipt{}, ErrInvalid
	}
	if _, e := m.authorize(ctx, tx, p, "test_run_execute", false); e != nil {
		return Receipt{}, e
	}
	row, e := m.planOn(ctx, tx, p, id)
	if e != nil {
		return Receipt{}, e
	}
	if row.Version != v || row.State != "FROZEN" {
		return Receipt{}, ErrConflict
	}
	plan, e := planView(row)
	if e != nil {
		return Receipt{}, e
	}
	var frozen *TestCaseView
	found := false
	for _, c := range plan.Cases {
		if c.ID == in.CaseID && c.Version == in.CaseVersion {
			found = true
			snapshot := c
			frozen = &snapshot
		}
	}
	if !found {
		return Receipt{}, ErrConflict
	}
	if in.Defect != nil && (in.Result != "FAILED" || in.DefectID != nil) {
		return Receipt{}, ErrInvalid
	}
	if in.DefectID != nil {
		if e = m.projects.ValidateWorkOn(ctx, tx, row.TenantID, p, *in.DefectID, true); e != nil {
			return Receipt{}, e
		}
	}
	rid, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	if in.Defect != nil {
		if m.testDefects == nil || frozen == nil {
			return Receipt{}, ErrUnavailable
		}
		defect := *in.Defect
		defect.PlanID, defect.PlanVersion, defect.RunID, defect.CaseID, defect.CaseVersion = row.ID, row.Version, rid, frozen.ID, frozen.Version
		defect.Steps, defect.Expected, defect.Observed = frozen.Steps, frozen.Expected, in.Evidence
		defectID, err := m.testDefects.CreateOn(ctx, tx, row.TenantID, p, defect)
		if err != nil {
			return Receipt{}, err
		}
		in.DefectID = &defectID
	}
	run := &model.TestRun{TenantID: row.TenantID, ID: rid, ProjectID: p, PlanID: id, CaseID: in.CaseID, CaseVersion: in.CaseVersion, Result: in.Result, Evidence: in.Evidence, DefectID: in.DefectID, ActorID: authn.PrincipalIDFromContext(ctx), CreatedAt: now()}
	if e = query.BindTestRun(tx, m.names).WithContext(ctx).Create(run); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, rid, "test_run_recorded", "test_run_execute", 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: rid, Version: 1, State: in.Result}, nil
}
