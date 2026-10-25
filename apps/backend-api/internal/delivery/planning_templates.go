package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"database/sql/driver"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

func (m *Module) registerInbox(s *module.Services) error {
	sources, err := module.Resolve[governance.InboxSources](s)
	if err != nil {
		return err
	}
	return sources.Register(governance.InboxSource{Kind: "WORK_ITEM_REVIEW", Read: m.reviewInbox})
}

// reviewInbox exposes only actual current result versions needing an
// independent manual CHECK. Automatic-verdict overrides remain unavailable.
func (m *Module) reviewInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	out := []governance.InboxItem{}
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return out, ErrWorkForbidden
	}
	if req.Kind != "WORK_ITEM_REVIEW" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return out, ErrInvalidArgument
	}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return out, governance.ErrUnavailable
	}
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		w := query.BindWorkItems(tx, m.service.names)
		rows, e := w.WithContext(ctx).Where(w.TenantID.Eq(req.TenantID), w.State.Eq("NEEDS_HUMAN_REVIEW"), w.ArchivedAt.IsNull()).Find()
		if e != nil {
			return e
		}
		for _, candidate := range rows {
			if candidate.CurrentClaimID == nil || (req.ProjectID != 0 && candidate.ProjectID != req.ProjectID) {
				continue
			}
			work, result, e := m.lockResultOn(ctx, tx, req.TenantID, candidate.ProjectID, candidate.ID, *candidate.CurrentClaimID, req.PrincipalID, candidate.AccountablePrincipalID, candidate.Version, "work_item_write")
			if errors.Is(e, ErrWorkForbidden) || errors.Is(e, ErrInvalidMember) || errors.Is(e, ErrProjectNotFound) || errors.Is(e, ErrWorkProjectReadOnly) || errors.Is(e, ErrResultState) {
				continue
			}
			if e != nil {
				return e
			}
			if result.ActorID == req.PrincipalID {
				continue
			}
			// The canonical source must also be readable under current IAM.
			if _, _, _, e = m.lockedWorkOn(ctx, tx, req.TenantID, work.ProjectID, work.ID, req.PrincipalID, work.AccountablePrincipalID, "work_item_read"); errors.Is(e, ErrWorkForbidden) || errors.Is(e, ErrProjectNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			a := query.BindResultActions(tx, m.service.names)
			count, e := a.WithContext(ctx).Where(a.TenantID.Eq(req.TenantID), a.ProjectID.Eq(work.ProjectID), a.ResultID.Eq(result.ID), a.Kind.Eq("CHECK")).Count()
			if e != nil {
				return e
			}
			if count > 0 {
				continue
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "NORMAL", CreatedAt: result.CreatedAt, Kind: req.Kind, ID: result.ID}, ProjectID: work.ProjectID, Title: work.Title, ObjectType: "work_item", ObjectID: work.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
			if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
				out = append(out, item)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b governance.InboxItem) int { return governance.CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

// ProjectScope fences a caller-owned transaction using current project policy.
type ProjectScope struct {
	ArchivedOn            func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
	AuthorizeOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
	ValidateTriageAgentOn func(context.Context, *gorm.DB, guid.ID, guid.ID) error
}
type TemplateCriterion struct{ Text, VerificationMethod string }
type TemplateWorkInput struct {
	Title, Description, Type, Objective string
	AccountablePrincipalID              guid.ID
	Criteria                            []TemplateCriterion
}
type TemplateInstantiation struct {
	AddDependencyOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
	CreateWorkOn             func(context.Context, *gorm.DB, guid.ID, guid.ID, TemplateWorkInput) (guid.ID, error)
	InitializeConstitutionOn func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (guid.ID, error)
	WorkStatusOn             func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error)
}

// PlanningWorkFact preserves accepted timestamps; later acceptance never counts
// as completion in an earlier reporting period.
type PlanningWorkFact struct {
	ID                     guid.ID    `json:"id"`
	Title                  string     `json:"title"`
	State                  string     `json:"state"`
	AccountablePrincipalID guid.ID    `json:"accountable_principal_id"`
	AcceptedAt             *time.Time `json:"accepted_at"`
	ResultID               *guid.ID   `json:"result_id"`
}
type PlanningSnapshot struct {
	QualityMetrics      *ReportQualityMetrics `json:"quality_metrics,omitempty"`
	ExecutionCostMicros *int64                `json:"execution_cost_micros,omitempty"`
	ExecutionCreated    *int64                `json:"execution_created,omitempty"`
	ExecutionStates     map[string]int64      `json:"execution_states,omitempty"`
	Work                []PlanningWorkFact    `json:"work"`
	ResultCount         int64                 `json:"result_count"`
	HandoffCount        int64                 `json:"handoff_count"`
	MissingSources      []string              `json:"missing_sources"`
}
type PlanningFacts struct {
	ProjectsOn func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error)
	ReadOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (PlanningSnapshot, error)
}

func (m *Module) providePlanningTemplates(s *module.Services) error {
	if err := m.provideIterationWork(s); err != nil {
		return err
	}
	if err := module.Provide[ProjectScope](s, ProjectScope{ArchivedOn: m.archiveProjectStateOn, AuthorizeOn: m.authorizeOn, ValidateTriageAgentOn: m.validateTriageAgentOn}); err != nil {
		return err
	}
	if err := module.Provide[PlanningFacts](s, PlanningFacts{ReadOn: m.planningFactsOn, ProjectsOn: m.planningProjectsOn}); err != nil {
		return err
	}
	return module.Provide[TemplateInstantiation](s, TemplateInstantiation{AddDependencyOn: m.templateDependencyOn, CreateWorkOn: m.templateWorkOn, InitializeConstitutionOn: m.templateConstitutionOn, WorkStatusOn: m.templateWorkStatusOn})
}
func (m *Module) authorizeOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	if authn.TenantIDFromContext(ctx) != tenant {
		return ErrWorkForbidden
	}
	if strings.HasSuffix(permission, "_read") {
		return m.projectHistoryAuthorityOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), permission)
	}
	_, _, err := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), permission)
	return err
}
func (m *Module) templateWorkOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in TemplateWorkInput) (guid.ID, error) {
	criteria := make([]workCriterionInput, len(in.Criteria))
	for i, c := range in.Criteria {
		criteria[i] = workCriterionInput(c)
	}
	v, e := m.createWorkOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), workCreateBody{Title: in.Title, Description: in.Description, Type: in.Type, Objective: in.Objective, AccountablePrincipalID: in.AccountablePrincipalID, Criteria: criteria})
	return v.ID, e
}
func (m *Module) templateConstitutionOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, document string) (guid.ID, error) {
	v, e := m.proposeConstitutionOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), constitutionProposalBody{Body: document, ChangeSummary: "Initialize constitution from template"})
	return v.ID, e
}
func (m *Module) templateWorkStatusOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (string, error) {
	if e := m.authorizeOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
		return "", e
	}
	c, e := m.workPolicy(ctx, tx, tenant, project, "work_item_read", authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return "", e
	}
	q := query.BindWorkItems(tx, m.service.names)
	scope, e := workScope(c, project, q.CreatedBy)
	if e != nil {
		return "", e
	}
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	// Advance may already have a repeatable-read snapshot. Use the current
	// locked row after the project fence so reopened work cannot release a node.
	r, e := base.Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if e != nil {
		return "", e
	}
	return r.State, nil
}
func (m *Module) planningFactsOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, start, end time.Time) (PlanningSnapshot, error) {
	out := PlanningSnapshot{Work: []PlanningWorkFact{}, MissingSources: []string{"EXECUTIONS", "EXECUTION_COST", "AUTOMATIC_VERIFICATION", "CLIENT_ACCEPTANCE", "QUALITY_METRICS"}}
	if !start.Before(end) {
		return out, ErrInvalidArgument
	}
	if e := m.authorizeOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
		return out, e
	}
	q := query.BindWorkItems(tx, m.service.names)
	c, e := m.workPolicy(ctx, tx, tenant, project, "work_item_read", authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return out, e
	}
	scope, e := workScope(c, project, q.CreatedBy)
	if e != nil {
		return out, e
	}
	d := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull(), q.CreatedAt.Lt(end))
	if scope != nil {
		d = d.Where(scope)
	}
	if subject > 0 {
		d = d.Where(q.AccountablePrincipalID.Eq(subject))
	}
	rows, e := d.Order(q.ID).Limit(501).Find()
	if e != nil {
		return out, e
	}
	if len(rows) > 500 {
		return out, ErrInvalidArgument
	}
	for _, r := range rows {
		out.Work = append(out.Work, PlanningWorkFact{ID: r.ID, Title: r.Title, State: r.State, AccountablePrincipalID: r.AccountablePrincipalID, AcceptedAt: r.AcceptedAt, ResultID: r.CurrentClaimID})
	}
	// Counts are scoped by actual owned work rows, preventing another member's
	// result or handoff from leaking into a member report.
	ids := make([]driver.Valuer, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	if len(ids) == 0 {
		return out, nil
	}
	rv := query.BindResultVersions(tx, m.service.names)
	out.ResultCount, e = rv.WithContext(ctx).Where(rv.TenantID.Eq(tenant), rv.ProjectID.Eq(project), rv.WorkItemID.In(ids...), rv.CreatedAt.Gte(start), rv.CreatedAt.Lt(end)).Count()
	if e != nil {
		return out, e
	}
	h := query.BindResultHandoffs(tx, m.service.names)
	out.HandoffCount, e = h.WithContext(ctx).Where(h.TenantID.Eq(tenant), h.ProjectID.Eq(project), h.WorkItemID.In(ids...), h.CreatedAt.Gte(start), h.CreatedAt.Lt(end)).Count()
	return out, e
}

func (m *Module) templateDependencyOn(ctx context.Context, tx *gorm.DB, tenant, project, work, predecessor guid.ID) error {
	if err := m.authorizeOn(ctx, tx, tenant, project, "work_item_write"); err != nil {
		return err
	}
	// Template work can have been created earlier in this very transaction.
	// preparationOwner intentionally accepts only a pre-transaction handle;
	// resolve this lock set through the caller's snapshot instead.
	w := query.BindWorkItems(tx, m.service.names)
	row, err := w.WithContext(ctx).Select(w.AccountablePrincipalID).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(work), w.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrProjectNotFound
	}
	if err != nil {
		return err
	}
	_, err = m.changeDependencyOn(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx), row.AccountablePrincipalID, predecessor, 0, false)
	return err
}

// planningProjectsOn enumerates current authorized projects, never projects
// inferred solely from reports that happened to have been submitted.
func (m *Module) planningProjectsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]guid.ID, error) {
	if authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrWorkForbidden
	}
	q := query.BindProjects(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ArchivedAt.IsNull()).Order(q.ProjectID).Limit(501).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 500 {
		return nil, ErrUnavailable
	}
	out := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		if err := m.authorizeOn(ctx, tx, tenant, row.ProjectID, "work_item_read"); err != nil {
			if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, row.ProjectID)
	}
	return out, nil
}
