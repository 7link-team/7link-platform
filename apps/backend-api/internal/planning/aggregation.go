package planning

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// PlanningChildReportRef preserves the provenance of each generated aggregate.
// Child state and content are checked again when submitting or confirming.
type PlanningChildReportRef struct {
	ID          guid.ID `json:"id"`
	ContentHash string  `json:"content_hash"`
}
type PlanningAggregationRequest struct {
	ProjectID   guid.ID `json:"project_id"`
	Scope       string  `json:"scope" enum:"PROJECT,TEAM"`
	PeriodKind  string  `json:"period_kind" enum:"DAY,WEEK,MONTH,QUARTER,YEAR"`
	PeriodStart string  `json:"period_start" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
}
type PlanningAggregationHTTPInput struct {
	PlanningTenantInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           PlanningAggregationRequest
}

func (m *Module) registerAggregationHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "planning.report.aggregate", Method: "POST", Path: "/reports/aggregations", Summary: "Generate a project or team report from current facts and child reports", Tags: []string{"tenants"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "report", Verb: "submit"}, func(ctx context.Context, in *PlanningAggregationHTTPInput) (*PlanningCommandOutput, error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, planningError(ctx, ErrInvalid)
		}
		return m.command(ctx, tenant, in.IdempotencyKey, "report.aggregate", "/reports/aggregations", in.Body, func(tx *gorm.DB) (PlanningRecord, error) { return m.createAggregationOn(ctx, tx, tenant, in.Body) })
	})
}
func reportAuthor(r *model.Report) guid.ID {
	if r.PrincipalID > 0 {
		return r.PrincipalID
	}
	return r.CreatedBy
}
func reportPeriod(r *model.Report) (time.Time, time.Time, error) {
	var body PlanningReportInput
	if err := json.Unmarshal([]byte(r.Content), &body); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if body.BoundaryStart != nil && body.BoundaryEnd != nil && body.BoundaryStart.Before(*body.BoundaryEnd) {
		return *body.BoundaryStart, *body.BoundaryEnd, nil
	}
	// Existing reports retain their original UTC boundary; new reports persist it.
	return period(r.PeriodKind, r.PeriodStart)
}
func (m *Module) reportFactsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report, start, end time.Time) (delivery.PlanningSnapshot, error) {
	if r.ProjectID > 0 {
		return m.snapshotOn(ctx, tx, tenant, r.ProjectID, r.PrincipalID, start, end)
	}
	if m.facts.ProjectsOn == nil {
		return delivery.PlanningSnapshot{}, ErrUnavailable
	}
	projects, err := m.facts.ProjectsOn(ctx, tx, tenant)
	if err != nil {
		return delivery.PlanningSnapshot{}, err
	}
	out := delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{}, ExecutionStates: map[string]int64{}, MissingSources: []string{}}
	var executionCount int64
	var executionCost int64
	costAvailable := true
	executionAvailable := true
	seen := map[guid.ID]bool{}
	for _, project := range projects {
		facts, err := m.snapshotOn(ctx, tx, tenant, project, r.PrincipalID, start, end)
		if err != nil {
			return out, err
		}
		for _, work := range facts.Work {
			if !seen[work.ID] {
				out.Work = append(out.Work, work)
				seen[work.ID] = true
			}
		}
		if facts.QualityMetrics != nil {
			if out.QualityMetrics == nil {
				out.QualityMetrics = &delivery.ReportQualityMetrics{}
			}
			out.QualityMetrics.Add(facts.QualityMetrics)
		}
		out.ResultCount += facts.ResultCount
		out.HandoffCount += facts.HandoffCount
		for _, source := range facts.MissingSources {
			if !slices.Contains(out.MissingSources, source) {
				out.MissingSources = append(out.MissingSources, source)
			}
		}
		if facts.ExecutionCreated == nil {
			executionAvailable = false
		} else {
			executionCount += *facts.ExecutionCreated
		}
		if facts.ExecutionCostMicros == nil {
			costAvailable = false
		} else {
			if *facts.ExecutionCostMicros < 0 || executionCost > int64(^uint64(0)>>1)-*facts.ExecutionCostMicros {
				return out, ErrUnavailable
			}
			executionCost += *facts.ExecutionCostMicros
		}
		for state, count := range facts.ExecutionStates {
			out.ExecutionStates[state] += count
		}
	}
	if slices.Contains(out.MissingSources, "QUALITY_METRICS") {
		out.QualityMetrics = nil
	}
	if len(out.Work) > 500 {
		return out, ErrUnavailable
	}
	if executionAvailable {
		out.ExecutionCreated = &executionCount
	}
	if costAvailable {
		out.ExecutionCostMicros = &executionCost
	}
	return out, nil
}
func (m *Module) childReportsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report) ([]*model.Report, error) {
	q := query.BindReports(tx, m.names)
	rows := []*model.Report{}
	if r.PrincipalID == 0 {
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Neq(r.ID), q.PeriodKind.Eq(r.PeriodKind), q.PeriodStart.Eq(r.PeriodStart))
		if r.ProjectID > 0 {
			base = base.Where(q.ProjectID.Eq(r.ProjectID), q.PrincipalID.Gt(guid.ID(0)))
		} else {
			base = base.Where(q.PrincipalID.Eq(guid.ID(0)), q.ProjectID.Gt(guid.ID(0)))
		}
		found, err := base.Order(q.ID).Limit(501).Find()
		if err != nil {
			return nil, err
		}
		rows = append(rows, found...)
	}
	lower := map[string]string{"WEEK": "DAY", "MONTH": "WEEK", "QUARTER": "MONTH", "YEAR": "QUARTER"}[r.PeriodKind]
	if lower != "" {
		start, end, err := reportPeriod(r)
		if err != nil {
			return nil, err
		}
		// Include overlapping cross-month weeks; acceptance timestamps decide which
		// child completions belong to this aggregate, never whole-week sums.
		found, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Neq(r.ID), q.ProjectID.Eq(r.ProjectID), q.PrincipalID.Eq(r.PrincipalID), q.PeriodKind.Eq(lower), q.PeriodStart.Gte(start.AddDate(0, 0, -8).Format("2006-01-02")), q.PeriodStart.Lt(end.Add(24*time.Hour).Format("2006-01-02"))).Order(q.ID).Limit(501).Find()
		if err != nil {
			return nil, err
		}
		for _, child := range found {
			a, b, err := reportPeriod(child)
			if err != nil {
				return nil, err
			}
			if a.Before(end) && b.After(start) {
				rows = append(rows, child)
			}
		}
	}
	// Scheduled MEMBER reports have tenant-wide identity. Preserve explicitly
	// recorded project provenance rather than requiring duplicate reports for
	// each project the same member contributed to.
	if r.PrincipalID == 0 {
		var body PlanningReportInput
		if err := json.Unmarshal([]byte(r.Content), &body); err != nil {
			return nil, err
		}
		for _, ref := range body.Children {
			if slices.ContainsFunc(rows, func(row *model.Report) bool { return row.ID == ref.ID }) {
				continue
			}
			child, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(ref.ID), q.ProjectID.Eq(guid.ID(0)), q.PrincipalID.Gt(guid.ID(0)), q.PeriodKind.Eq(r.PeriodKind), q.PeriodStart.Eq(r.PeriodStart)).First()
			if err != nil {
				return nil, err
			}
			rows = append(rows, child)
		}
	}
	if len(rows) > 500 {
		return nil, ErrUnavailable
	}
	for _, row := range rows {
		if _, err := m.authorize(ctx, tx, tenant, row.ProjectID, row.PrincipalID, "report_read", false); err != nil {
			return nil, err
		}
	}
	return rows, nil
}
func (m *Module) reportLinksOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report, body *PlanningReportInput) error {
	// The body is available before insertion and contains the fixed local bounds.
	content, _, err := hashJSON(body)
	if err != nil {
		return err
	}
	r.Content = content
	children, err := m.childReportsOn(ctx, tx, tenant, r)
	if err != nil {
		return err
	}
	body.Children = []PlanningChildReportRef{}
	for _, child := range children {
		body.Children = append(body.Children, PlanningChildReportRef{ID: child.ID, ContentHash: child.ContentHash})
	}
	return nil
}
func (m *Module) createAggregationOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in PlanningAggregationRequest) (PlanningRecord, error) {
	var out PlanningRecord
	if (in.Scope != "PROJECT" && in.Scope != "TEAM") || (in.Scope == "PROJECT" && in.ProjectID <= 0) || (in.Scope == "TEAM" && in.ProjectID != 0) {
		return out, ErrInvalid
	}
	actorKind, err := m.authorize(ctx, tx, tenant, in.ProjectID, 0, "report_submit", true)
	if err != nil {
		return out, err
	}
	start, end, err := m.periodForOn(ctx, tx, tenant, in.PeriodKind, in.PeriodStart)
	if err != nil {
		return out, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	r := &model.Report{TenantID: tenant, ID: guid.ID(id), ProjectID: in.ProjectID, PrincipalID: 0, PeriodKind: in.PeriodKind, PeriodStart: in.PeriodStart, State: "DRAFT", Version: 1, CreatedBy: actor, CreatedAt: m.now(), UpdatedAt: m.now()}
	facts, err := m.reportFactsOn(ctx, tx, tenant, r, start, end)
	if err != nil {
		return out, err
	}
	settings, err := m.readSettingsOn(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	body := PlanningReportInput{CalendarTimezone: settings.Timezone, Scope: in.Scope, BoundaryStart: &start, BoundaryEnd: &end, Completed: []PlanningCompletion{}, Children: []PlanningChildReportRef{}, NextPlan: []PlanningPlanItem{}}
	for _, fact := range facts.Work {
		if fact.State == "ACCEPTED" && fact.AcceptedAt != nil && !fact.AcceptedAt.Before(start) && fact.AcceptedAt.Before(end) {
			body.Completed = append(body.Completed, PlanningCompletion{WorkItemID: fact.ID})
		}
	}
	if err := m.reportLinksOn(ctx, tx, tenant, r, &body); err != nil {
		return out, err
	}
	r.Content, r.ContentHash, err = hashJSON(body)
	if err != nil {
		return out, err
	}
	r.Facts, _, err = hashJSON(facts)
	if err != nil {
		return out, err
	}
	if err = m.initializeReportDueOn(ctx, tx, tenant, r); err != nil {
		return out, err
	}
	if err = query.BindReports(tx, m.names).WithContext(ctx).Create(r); err != nil {
		return out, err
	}
	out, err = reportView(r)
	if err != nil {
		return out, err
	}
	return out, m.record(ctx, tx, tenant, out, actorKind, "CREATE", "")
}
func (m *Module) validateReportItems(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report, items []PlanningPlanItem) error {
	if r.PrincipalID > 0 {
		if r.ProjectID == 0 {
			if len(items) < 1 || len(items) > 100 {
				return ErrInvalid
			}
			q := query.BindGoals(tx, m.names)
			for _, item := range items {
				goal, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(item.GoalID), q.PrincipalID.Eq(r.PrincipalID)).First()
				if err != nil {
					return err
				}
				if err := m.validateItems(ctx, tx, tenant, goal.ProjectID, r.PrincipalID, []PlanningPlanItem{item}); err != nil {
					return err
				}
			}
			return nil
		}
		return m.validateItems(ctx, tx, tenant, r.ProjectID, r.PrincipalID, items)
	}
	if len(items) < 1 || len(items) > 100 {
		return ErrInvalid
	}
	expected := "PROJECT"
	if r.ProjectID == 0 {
		expected = "TEAM"
	}
	for _, item := range items {
		goal, g, err := m.goalOn(ctx, tx, tenant, r.ProjectID, item.GoalID)
		if err != nil {
			return err
		}
		if !validText(item.Title, 200) || goal.State != "ACTIVE" || g.Level != expected {
			return ErrInvalid
		}
		found := false
		for _, kr := range g.KeyResults {
			found = found || kr.Key == item.KR
		}
		if !found {
			return ErrInvalid
		}
		if item.WorkItemID != nil {
			if r.ProjectID == 0 {
				return ErrInvalid
			}
			facts, err := m.snapshotOn(ctx, tx, tenant, r.ProjectID, 0, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), m.now().Add(time.Millisecond))
			if err != nil {
				return err
			}
			found = false
			for _, work := range facts.Work {
				found = found || work.ID == *item.WorkItemID
			}
			if !found {
				return ErrInvalid
			}
		} else if !slices.Contains([]string{"LEARNING", "OPS", "REVIEW", "SUPPORT", "OTHER"}, item.Category) {
			return ErrInvalid
		}
	}
	return nil
}
func (m *Module) checkReportChildrenOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report, out *PlanningCheck) error {
	children, err := m.childReportsOn(ctx, tx, tenant, r)
	if err != nil {
		return err
	}
	var body PlanningReportInput
	if err = json.Unmarshal([]byte(r.Content), &body); err != nil {
		return err
	}
	refs := map[guid.ID]string{}
	for _, ref := range body.Children {
		if refs[ref.ID] != "" {
			return ErrInvalid
		}
		refs[ref.ID] = ref.ContentHash
	}
	add := func(code string, blocking bool) {
		out.Issues = append(out.Issues, PlanningIssue{Code: code, Blocking: blocking})
		if blocking {
			out.Result = "BLOCKED"
		} else if out.Result == "PASS" {
			out.Result = "PASS_WITH_FLAGS"
		}
	}
	claims := map[guid.ID]bool{}
	for _, claim := range body.Completed {
		claims[claim.WorkItemID] = true
	}
	for _, child := range children {
		if refs[child.ID] != child.ContentHash {
			add("N4_CHILD_CHANGED", false)
		}
		delete(refs, child.ID)
		if child.State != "SUBMITTED" && child.State != "CONFIRMED" {
			add("N1_CHILD_UNSUBMITTED", false)
			continue
		}
		if child.State != "CONFIRMED" {
			add("N2_CHILD_UNCONFIRMED", false)
		}
		var content PlanningReportInput
		if err = json.Unmarshal([]byte(child.Content), &content); err != nil {
			return err
		}
		for _, completion := range content.Completed {
			if r.ProjectID > 0 && child.ProjectID == 0 && !slices.ContainsFunc(out.Facts.Work, func(work delivery.PlanningWorkFact) bool { return work.ID == completion.WorkItemID }) {
				continue
			}
			outside := false
			start, end, err := reportPeriod(r)
			if err != nil {
				return err
			}
			for _, fact := range out.Facts.Work {
				if fact.ID == completion.WorkItemID && fact.AcceptedAt != nil {
					outside = fact.AcceptedAt.Before(start) || !fact.AcceptedAt.Before(end)
				}
			}
			if !claims[completion.WorkItemID] && !outside {
				add("B2_AGGREGATE_MISMATCH", true)
			}
		}
	}
	if len(refs) > 0 {
		return ErrConflict
	}
	return nil
}
