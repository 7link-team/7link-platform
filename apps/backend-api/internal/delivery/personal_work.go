package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm/clause"
	"net/http"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

type personalWorkInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor   string `query:"cursor" maxLength:"512"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}

type personalTenantWorkInput struct {
	Cursor string `query:"cursor" maxLength:"512"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}

// PersonalWorkView projects existing responsibility and current lease facts.
// It does not create a second task, assignment, review or acceptance record.
type PersonalWorkView struct {
	WorkView
	Responsibility string `json:"responsibility" enum:"accountable,executor,both"`
}

type PersonalWorkPage struct {
	Items      []PersonalWorkView `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
	HasMore    bool               `json:"has_more"`
}

// SelectedAgentWork reads actual responsibility and live assignments for an
// Agent. Authorization always belongs to the viewing principal, never the Agent.
type SelectedAgentWork struct {
	ListOn func(context.Context, *gorm.DB, guid.ID, guid.ID, string, int) (PersonalWorkPage, error)
}

func (m *Module) selectedAgentWorkOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, cursor string, limit int) (PersonalWorkPage, error) {
	out := PersonalWorkPage{Items: []PersonalWorkView{}}
	viewer := authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || agent <= 0 || viewer <= 0 || authn.TenantIDFromContext(ctx) != tenant || limit < 1 || limit > 100 || tx == nil || tx.Statement == nil {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.listConstraint == nil {
		return out, ErrUnavailable
	}
	after, e := personalWorkPosition(cursor, tenant, agent, viewer)
	if e != nil {
		return out, e
	}
	c, e := m.listConstraint(ctx, tenant, "work_item_read", viewer)
	if e != nil {
		return out, e
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, e := personalWorkScope(c, 0, w.ProjectID, w.CreatedBy)
	if e != nil {
		return out, e
	}
	a := query.BindAssignments(tx, m.service.names)
	p := query.BindProjects(tx, m.service.names)
	live := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.AssigneePrincipalID.Eq(agent), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE"), a.EndedAt.IsNull(), a.ActiveSlot.EqCol(w.ID), a.WorkItemID.EqCol(w.ID), a.ProjectID.EqCol(w.ProjectID)).Where(a.WithContext(ctx).Where(a.LeaseExpiresAt.IsNull()).Or(a.LeaseExpiresAt.Gt(time.Now().UTC())))
	project := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.EqCol(w.ProjectID), p.ArchivedAt.IsNull())
	base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ArchivedAt.IsNull(), gen.Exists(project)).Where(w.WithContext(ctx).Where(w.AccountablePrincipalID.Eq(agent)).Or(gen.Exists(live)))
	if scope != nil {
		base = base.Where(scope)
	}
	if after > 0 {
		base = base.Where(w.ID.Lt(after))
	}
	rows, e := base.Order(w.ID.Desc()).Limit(limit + 1).Find()
	if e != nil {
		return out, e
	}
	out.HasMore = len(rows) > limit
	if out.HasMore {
		rows = rows[:limit]
	}
	for _, r := range rows {
		// The correlated existence predicate was evaluated before pagination.
		role := "executor"
		if r.AccountablePrincipalID == agent {
			role = "accountable"
		}
		out.Items = append(out.Items, PersonalWorkView{WorkView: workRecord(r), Responsibility: role})
	}
	if out.HasMore {
		out.NextCursor = personalWorkCursor(tenant, agent, viewer, rows[len(rows)-1].ID)
	}
	return out, nil
}

func (m *Module) registerPersonalWorkHTTP(api huma.API) {
	iamauthn.RegisterCancellationReauthentication(api)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.get", Method: "GET", Path: workResultPath, Summary: "Read one authorized immutable result and its evidence", Tags: []string{"work-items"}, Errors: []int{401, 403, 404, 422, 500, 503}}, "work_item_id", authz.ResourceOwnerResolver(m.workOwner), authz.Guard{Resource: "work_item", Verb: "read"}, m.resultHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.personal", Method: http.MethodGet, Path: "/entities/{entity_id}/my-work", Summary: "List current principal work in a project", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "work_item", Verb: "read"}, m.personalWorkHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "work_item.personal.all", Method: http.MethodGet, Path: "/my-work", Summary: "List current principal work across authorized projects", Tags: []string{"tenants"}, Errors: []int{401, 403, 422, 500, 503}}, authz.Guard{Resource: "work_item", Verb: "read", FilterData: true}, m.personalTenantWorkHTTP)
}

func personalWorkCursor(tenant, project, actor, last guid.ID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join([]string{tenant.String(), project.String(), actor.String(), last.String()}, ":")))
}

func personalWorkPosition(raw string, tenant, project, actor guid.ID) (guid.ID, error) {
	if raw == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, ErrInvalidArgument
	}
	parts := strings.Split(string(b), ":")
	if len(parts) != 4 || parts[0] != tenant.String() || parts[1] != project.String() || parts[2] != actor.String() {
		return 0, ErrInvalidArgument
	}
	last, err := guid.RequireID(parts[3])
	if err != nil {
		return 0, ErrInvalidArgument
	}
	return last, nil
}

func (m *Module) personalWorkHTTP(ctx context.Context, in *personalWorkInput) (*respx.Body[PersonalWorkPage], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	return m.readPersonalWorkHTTP(ctx, project, in.Cursor, in.Limit)
}

func (m *Module) personalTenantWorkHTTP(ctx context.Context, in *personalTenantWorkInput) (*respx.Body[PersonalWorkPage], error) {
	return m.readPersonalWorkHTTP(ctx, 0, in.Cursor, in.Limit)
}

// personalWorkScope applies the same entity-denial/owner-row semantics as
// workScope before keyset pagination, including for the tenant-wide projection.
func personalWorkScope(c authz.DataConstraint, project guid.ID, projectID, owner field.Field) (field.Expr, error) {
	if project > 0 {
		return workScope(c, project, owner)
	}
	parse := func(raw []string) ([]driver.Valuer, error) {
		ids := make([]driver.Valuer, 0, len(raw))
		for _, value := range raw {
			id, err := projectFilterID(value)
			if err != nil {
				return nil, ErrUnavailable
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	denied, err := parse(c.DeniedIDs)
	if err != nil {
		return nil, err
	}
	var predicates []field.Expr
	if len(denied) > 0 {
		predicates = append(predicates, projectID.NotIn(denied...))
	}
	if !c.AllowAll {
		resources, err := parse(c.ResourceIDs)
		if err != nil {
			return nil, err
		}
		owners, err := parse(c.OwnerIDs)
		if err != nil {
			return nil, err
		}
		var grants []field.Expr
		if len(resources) > 0 {
			grants = append(grants, projectID.In(resources...))
		}
		if len(owners) > 0 {
			grants = append(grants, owner.In(owners...))
		}
		if len(grants) == 0 {
			return projectID.Eq(guid.ID(0)), nil
		}
		if len(grants) == 1 {
			predicates = append(predicates, grants[0])
		} else {
			predicates = append(predicates, field.Or(grants...))
		}
	}
	if len(predicates) == 0 {
		return nil, nil
	}
	return field.And(predicates...), nil
}

func (m *Module) readPersonalWorkHTTP(ctx context.Context, project guid.ID, cursor string, limit int) (*respx.Body[PersonalWorkPage], error) {
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || actor <= 0 || limit < 1 || limit > 100 {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	after, err := personalWorkPosition(cursor, tenant, project, actor)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.service == nil || m.workPolicy == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	var constraint authz.DataConstraint
	if project == 0 {
		if m.listConstraint == nil {
			return nil, workHTTPError(ctx, ErrUnavailable)
		}
		constraint, err = m.listConstraint(ctx, tenant, "work_item_read", actor)
		if err != nil {
			return nil, workHTTPError(ctx, err)
		}
	}
	page := PersonalWorkPage{Items: make([]PersonalWorkView, 0)}
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if project > 0 {
			var e error
			constraint, e = m.workPolicy(ctx, tx, tenant, project, "work_item_read", actor)
			if e != nil {
				return e
			}
		}
		w := query.BindWorkItems(tx, m.service.names)
		scope, e := personalWorkScope(constraint, project, w.ProjectID, w.CreatedBy)
		if e != nil {
			return e
		}
		a := query.BindAssignments(tx, m.service.names)
		now := time.Now().UTC()
		active := func() query.IAssignmentDo {
			return a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.AssigneePrincipalID.Eq(actor), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE"), a.EndedAt.IsNull()).Where(a.WithContext(ctx).Where(a.LeaseExpiresAt.IsNull()).Or(a.LeaseExpiresAt.Gt(now)))
		}
		executing := active().Select(a.ID).Where(a.ActiveSlot.EqCol(w.ID), a.WorkItemID.EqCol(w.ID), a.ProjectID.EqCol(w.ProjectID))
		base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ArchivedAt.IsNull()).Where(w.WithContext(ctx).Where(w.AccountablePrincipalID.Eq(actor)).Or(gen.Exists(executing)))
		if project > 0 {
			base = base.Where(w.ProjectID.Eq(project))
		}
		if scope != nil {
			base = base.Where(scope)
		}
		if after > 0 {
			base = base.Where(w.ID.Lt(after))
		}
		rows, e := base.Order(w.ID.Desc()).Limit(limit + 1).Find()
		if e != nil {
			return e
		}
		page.HasMore = len(rows) > limit
		if page.HasMore {
			rows = rows[:limit]
		}
		workIDs := make([]driver.Valuer, 0, len(rows))
		for _, row := range rows {
			workIDs = append(workIDs, row.ID)
		}
		held := make(map[guid.ID]bool)
		if len(workIDs) > 0 {
			assignments, e := active().Where(a.WorkItemID.In(workIDs...)).Find()
			if e != nil {
				return e
			}
			for _, assignment := range assignments {
				if assignment.ActiveSlot != nil && *assignment.ActiveSlot == assignment.WorkItemID {
					held[assignment.WorkItemID] = true
				}
			}
		}
		for _, row := range rows {
			role := "accountable"
			if held[row.ID] {
				role = "executor"
				if row.AccountablePrincipalID == actor {
					role = "both"
				}
			}
			page.Items = append(page.Items, PersonalWorkView{WorkView: workRecord(row), Responsibility: role})
		}
		if page.HasMore {
			page.NextCursor = personalWorkCursor(tenant, project, actor, rows[len(rows)-1].ID)
		}
		return nil
	})
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return respx.OK(ctx, page), nil
}

// ClientWorkAccess delegates project locking and canonical baselines. Its
// methods run only inside the caller transaction; storage types stay private.
type ClientWorkAccess interface {
	TestCaseScopeOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClientRequirement, error)
	ExceptionProjectOn(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, string, string) error

	ApplyClientChangeOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string, ClientScopeChange) (guid.ID, error)
	ClientSummaryOn(context.Context, *gorm.DB, guid.ID, guid.ID) (ClientProjectSummary, error)
	LockProjectOn(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
	LockRequirementReadOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	RequirementsOn(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]ClientRequirement, error)
	SignRequirementsOn(context.Context, *gorm.DB, guid.ID, guid.ID, []ClientRequirement) error
	AcceptedArtifactsOn(context.Context, *gorm.DB, guid.ID, guid.ID, []ClientArtifactRef) ([]ClientArtifact, error)
	ApprovePreviewShareOn(context.Context, *gorm.DB, guid.ID, guid.ID) error
	ProgressArtifactOn(context.Context, *gorm.DB, guid.ID, guid.ID, ClientArtifactRef) (ClientArtifact, files.PinnedCandidate, error)
	SourceArtifactOn(context.Context, *gorm.DB, guid.ID, guid.ID, ClientSourceRef) (ClientArtifact, files.PinnedCandidate, error)
	ArtifactFileOn(context.Context, *gorm.DB, guid.ID, guid.ID, ClientArtifactRef) (files.PinnedCandidate, error)
	ValidateWorkOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) error
	SettingsOn(context.Context, *gorm.DB, guid.ID, guid.ID, *ClientSettingsPatch, int) (ClientSettings, error)
}
type ClientCriterion struct {
	ID       guid.ID `json:"id"`
	Code     string  `json:"code"`
	Text     string  `json:"text"`
	Required bool    `json:"required"`
	Position int     `json:"position"`
}
type ClientRequirement struct {
	ID          guid.ID           `json:"id"`
	Version     int               `json:"version"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Priority    int               `json:"priority"`
	Criteria    []ClientCriterion `json:"acceptance_criteria"`
}
type clientWork struct {
	m                 *Module
	cancellation      *ProjectExecutionCancellation
	dispatchExecution *DispatchExecutionAccess
	autoAuthority     governance.DispatchAutoAuthority
}

// ProjectExecutionCancellation is bound by Execution after all providers exist.
// DiscoverOn only discovers locks; CancelOn preserves runner acknowledgement.
type ProjectExecutionCancellation struct {
	PrincipalsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	CancelOn     func(context.Context, *gorm.DB, guid.ID, guid.ID) error
}
type ProjectCancellationConnector struct {
	Bind               func(ProjectExecutionCancellation) error
	AuthorizeReceiptOn func(context.Context, *gorm.DB, ExecutionWorkLink) error
}
type ProjectCancellationAccess interface {
	LockCancellationOn(context.Context, *gorm.DB, guid.ID, guid.ID) error
	CancelProjectOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, string, string) (string, error)
}

func (a clientWork) cancellationPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]guid.ID, error) {
	if a.cancellation == nil || a.cancellation.PrincipalsOn == nil {
		return nil, ErrUnavailable
	}
	ids, e := a.cancellation.PrincipalsOn(ctx, tx, tenant, project)
	if e != nil {
		return nil, e
	}
	w := query.BindWorkItems(tx, a.m.service.names)
	work, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ArchivedAt.IsNull()).Find()
	if e != nil {
		return nil, e
	}
	for _, row := range work {
		ids = append(ids, row.AccountablePrincipalID)
	}
	assignments := query.BindAssignments(tx, a.m.service.names)
	rows, e := assignments.WithContext(ctx).Where(assignments.TenantID.Eq(tenant), assignments.ProjectID.Eq(project), assignments.Status.Eq("ACTIVE")).Find()
	if e != nil {
		return nil, e
	}
	for _, row := range rows {
		ids = append(ids, row.AssigneePrincipalID)
	}
	ids = append(ids, authn.PrincipalIDFromContext(ctx))
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// LockCancellationOn must precede any actor/project locks in the command.
// Rechecking the discovered set under the project lock fences concurrent starts
// and assignment changes; a changed set is retried with a fresh transaction.
func (a clientWork) LockCancellationOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	if a.m == nil || a.m.service == nil || tx == nil || tx.Statement == nil || tenant != authn.TenantIDFromContext(ctx) || project <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	bindings, e := a.cancellationBindingsOn(ctx, tx, tenant, project, false)
	if e != nil {
		return e
	}
	ids, e := a.cancellationPrincipalsOn(ctx, tx, tenant, project)
	if e != nil {
		return e
	}
	if e = a.m.lockStagePrincipalsOn(ctx, tx, authn.PrincipalIDFromContext(ctx), ids); e != nil {
		return e
	}
	if e = a.LockProjectOn(ctx, tx, tenant, project, "exception_decide_replay"); e != nil {
		return e
	}
	current, e := a.cancellationBindingsOn(ctx, tx, tenant, project, true)
	if e != nil {
		return e
	}
	if bindings != current {
		return ErrWorkVersion
	}
	return nil
}

// A locking reread is essential under MySQL REPEATABLE READ: a second plain
// SELECT would reuse the old snapshot and miss a just-committed assignment or
// execution. Project serialization prevents any new binding after this fence.
func (a clientWork) cancellationBindingsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, locking bool) (string, error) {
	w := query.BindWorkItems(tx, a.m.service.names)
	works := w.WithContext(ctx).Select(w.ID, w.CurrentExecutionID, w.AccountablePrincipalID, w.Version).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ArchivedAt.IsNull()).Order(w.ID)
	q := query.BindAssignments(tx, a.m.service.names)
	assignments := q.WithContext(ctx).Select(q.ID, q.AssigneePrincipalID, q.Version).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Status.Eq("ACTIVE")).Order(q.ID)
	if locking {
		works = works.Clauses(clause.Locking{Strength: "UPDATE"})
		assignments = assignments.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	wr, e := works.Find()
	if e != nil {
		return "", e
	}
	ar, e := assignments.Find()
	if e != nil {
		return "", e
	}
	encoded, e := json.Marshal(struct {
		Work        []*model.WorkItem
		Assignments []*model.Assignment
	}{wr, ar})
	return string(encoded), e
}

// CancelProjectOn applies S12. The decision ID refers to the immutable command
// decision written by the caller in this transaction. Cancellation intent is
// recorded before work cancellation so unleased executions use normal cleanup.
func (a clientWork) CancelProjectOn(ctx context.Context, tx *gorm.DB, tenant, project, decisionID guid.ID, reason, cookie, proof string) (string, error) {
	if tx == nil || tx.Statement == nil || tenant != authn.TenantIDFromContext(ctx) || project <= 0 || decisionID <= 0 || len([]rune(strings.TrimSpace(reason))) < 10 || len([]rune(reason)) > 2000 {
		return "", ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return "", ErrInvalidArgument
	}
	if a.m == nil || a.m.service == nil || a.cancellation == nil || a.cancellation.CancelOn == nil || a.m.scopeReauthentication == nil || a.m.stageAuthority == nil {
		return "", ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if e := a.m.scopeReauthentication(ctx, tx, actor, cookie, proof, "project_cancel", "project:"+project.String()); e != nil {
		return "", e
	}
	row, kind, e := a.m.constitutionProjectOn(ctx, tx, tenant, project, actor, "exception_decide")
	if e != nil {
		return "", e
	}
	if kind != "HUMAN" || a.m.deliveryAuthority == nil {
		return "", ErrWorkForbidden
	}
	authority, e := a.m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: kind, Code: "exception_decide", Resource: governance.ResourceRef{Kind: "project", ID: project}})
	if e != nil {
		return "", e
	}
	if !authority.Allowed || authority.DelegationID != 0 || !strings.HasPrefix(authority.AuthoritySource, "ROLE:") {
		return "", ErrWorkForbidden
	}
	if _, _, e = a.m.constitutionProjectOn(ctx, tx, tenant, project, actor, "project_stage_change"); e != nil {
		return "", e
	}
	stageAuthority, e := a.m.stageAuthority(ctx, tx, tenant, project, actor, kind)
	if e != nil {
		return "", e
	}
	if !stageAuthority.Allowed || stageAuthority.DelegationID != 0 || !strings.HasPrefix(stageAuthority.AuthoritySource, "ROLE:") {
		return "", ErrWorkForbidden
	}
	if e = a.cancellation.CancelOn(ctx, tx, tenant, project); e != nil {
		return "", e
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	w := query.BindWorkItems(tx, a.m.service.names)
	work, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ArchivedAt.IsNull(), w.State.NotIn("ACCEPTED", "CANCELLED", "SKIPPED")).Order(w.ID).Find() //nolint:misspell // Frozen states.
	if e != nil {
		return "", e
	}
	for _, item := range work {
		before := item.State
		item.State = "CANCELLED" //nolint:misspell // Frozen state.
		item.CancelledAt = &at
		item.Version++
		item.StateChangedAt, item.UpdatedAt, item.UpdatedBy = at, at, &actor
		if _, e = w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(item.ID)).Select(w.State, w.CancelledAt, w.Version, w.StateChangedAt, w.UpdatedAt, w.UpdatedBy).Updates(item); e != nil {
			return "", e
		}
		id, e := a.m.ids.Next()
		if e != nil {
			return "", e
		}
		transition := &model.WorkItemTransition{TenantID: tenant, ID: guid.ID(id), ProjectID: project, WorkItemID: item.ID, FromState: &before, ToState: item.State, Trigger: "DECISION", ActorKind: kind, ActorID: &actor, AuthoritySource: authority.AuthoritySource, DecisionID: &decisionID, ReasonCode: "PROJECT_CANCELLED", VersionAfter: item.Version, CreatedAt: at} //nolint:misspell // Frozen reason.
		if e = query.BindWorkTransitions(tx, a.m.service.names).WithContext(ctx).Create(transition); e != nil {
			return "", e
		}
		_, e = a.m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.state_changed", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority.AuthoritySource}, Subject: events.Subject{Kind: "work_item", ID: item.ID}, SubjectVersion: int64(item.Version), Data: map[string]any{"from": before, "to": item.State, "decision_id": decisionID.String()}})
		if e != nil {
			return "", e
		}
	}
	assignments := query.BindAssignments(tx, a.m.service.names)
	activeAssignments, e := assignments.WithContext(ctx).Where(assignments.TenantID.Eq(tenant), assignments.ProjectID.Eq(project), assignments.Status.Eq("ACTIVE")).Order(assignments.ID).Find()
	if e != nil {
		return "", e
	}
	for _, assignment := range activeAssignments {
		assignment.Status, assignment.ActiveSlot, assignment.LeaseExpiresAt = "RELEASED", nil, nil
		assignment.EndedAt, assignment.EndReason, assignment.UpdatedAt, assignment.UpdatedBy = &at, &reason, at, &actor
		assignment.Version++
		if _, e = assignments.WithContext(ctx).Where(assignments.TenantID.Eq(tenant), assignments.ID.Eq(assignment.ID)).Select(assignments.Status, assignments.ActiveSlot, assignments.LeaseExpiresAt, assignments.EndedAt, assignments.EndReason, assignments.Version, assignments.UpdatedAt, assignments.UpdatedBy).Updates(assignment); e != nil {
			return "", e
		}
	}
	d := query.BindDeliveries(tx, a.m.service.names)
	drafts, e := d.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(d.TenantID.Eq(tenant), d.ProjectID.Eq(project), d.ArchivedAt.IsNull(), d.State.In("DRAFT", "READY")).Order(d.ID).Find()
	if e != nil {
		return "", e
	}
	for _, draft := range drafts {
		draft.State, draft.ArchivedAt, draft.UpdatedAt, draft.UpdatedBy = "DRAFT", &at, at, &actor
		draft.Version++
		if _, e = d.WithContext(ctx).Where(d.TenantID.Eq(tenant), d.ID.Eq(draft.ID)).Select(d.State, d.ArchivedAt, d.Version, d.UpdatedAt, d.UpdatedBy).Updates(draft); e != nil {
			return "", e
		}
	}
	before := row.Stage
	row.Stage = "CANCELLED" //nolint:misspell // Frozen state.
	row.BlockedKind, row.BlockedReason = nil, nil
	row.CancelledAt, row.CancelReason, row.StageChangedAt, row.Holds, row.Health, row.HealthReasons = &at, &reason, at, "[]", "NONE", "[]"
	row.Version++
	row.UpdatedAt, row.UpdatedBy = at, &actor
	p := query.BindProjects(tx, a.m.service.names)
	if _, e = p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).Select(p.Stage, p.BlockedKind, p.BlockedReason, p.CancelledAt, p.CancelReason, p.StageChangedAt, p.Holds, p.Health, p.HealthReasons, p.Version, p.UpdatedAt, p.UpdatedBy).Updates(row); e != nil {
		return "", e
	}
	id, e := a.m.ids.Next()
	if e != nil {
		return "", e
	}
	transition := &model.ProjectStageTransition{TenantID: tenant, ID: guid.ID(id), ProjectID: project, FromStage: before, ToStage: row.Stage, CriteriaSnapshot: "[]", DecisionID: &decisionID, Reason: reason, ActorKind: kind, ActorID: actor, AuthoritySource: authority.AuthoritySource, VersionAfter: row.Version, OccurredAt: at}
	if e = query.BindStageTransitions(tx, a.m.service.names).WithContext(ctx).Create(transition); e != nil {
		return "", e
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.project_cancelled", "project", project) //nolint:misspell // Frozen action.
	event.Detail["decision_id"], event.Detail["authority_source"], event.Detail["reason"] = decisionID.String(), authority.AuthoritySource, reason
	if e = a.m.service.audit(ctx, tx, event); e != nil {
		return "", e
	}
	_, e = a.m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "project.stage_changed", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority.AuthoritySource}, Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(row.Version), Data: map[string]any{"from_stage": before, "to_stage": row.Stage, "transition_id": transition.ID.String(), "decision_id": decisionID.String(), "reason": reason}})
	return authority.AuthoritySource, e
}

func (m *Module) provideClientWork(s *module.Services) error {
	if err := module.Provide[IntakeWorkFence](s, m.intakeWorkFenceOn); err != nil {
		return err
	}
	if err := module.Provide[IntakeWorkOwner](s, m.intakeWorkOwnerOn); err != nil {
		return err
	}
	if e := module.Provide[SelectedAgentWork](s, SelectedAgentWork{ListOn: m.selectedAgentWorkOn}); e != nil {
		return e
	}
	if e := module.Provide[IntakeAppender](s, IntakeAppender{NewRequirementOn: m.createIntakeRequirementOn, AppendFromIntake: m.appendFromIntakeOn}); e != nil {
		return e
	}
	a := &clientWork{m: m}
	if err := module.Provide[TestFailureDefects](s, a); err != nil {
		return err
	}
	m.stageCancellation = a
	if e := module.Provide[governance.DispatchAutoConsumer](s, governance.DispatchAutoConsumer{Bind: func(check governance.DispatchAutoAuthority) error {
		if check == nil || a.autoAuthority != nil {
			return ErrInvalidArgument
		}
		a.autoAuthority = check
		return nil
	}}); e != nil {
		return e
	}
	if e := module.Provide[DispatchExecutionConnector](s, DispatchExecutionConnector{Bind: func(c DispatchExecutionAccess) error {
		if a.dispatchExecution != nil || c.PrincipalsOn == nil || c.StopOn == nil || c.QueuesOn == nil {
			return ErrInvalidArgument
		}
		a.dispatchExecution = &c
		return nil
	}}); e != nil {
		return e
	}
	if e := module.Provide[ProjectCancellationConnector](s, ProjectCancellationConnector{AuthorizeReceiptOn: func(ctx context.Context, tx *gorm.DB, link ExecutionWorkLink) error {
		_, _, e := m.executionStopWorkOn(ctx, tx, link, false)
		return e
	}, Bind: func(c ProjectExecutionCancellation) error {
		if a.cancellation != nil || c.PrincipalsOn == nil || c.CancelOn == nil {
			return ErrInvalidArgument
		}
		a.cancellation = &c
		return nil
	}}); e != nil {
		return e
	}
	if e := module.Provide[DispatchAccess](s, DispatchAccess{HistorySamplesOn: a.dispatchHistorySamplesOn, AutoLockOn: a.dispatchAutoLockOn, AutoWorkOn: a.dispatchAutoWorkOn, AutoAuthorityOn: a.dispatchAutoAuthorityOn, AuthorityOn: a.dispatchAuthorityOn, WorkOn: a.dispatchWorkOn, LoadsOn: a.dispatchLoadsOn, ApplyOn: a.dispatchApplyOn, LockStopOn: a.dispatchLockStopOn, StopOn: a.dispatchStopOn, QueuesOn: a.dispatchQueuesOn}); e != nil {
		return e
	}
	if e := module.Provide[ForecastWorkFacts](s, ForecastWorkFacts{ReadOn: a.forecastWorkOn}); e != nil {
		return e
	}
	if e := module.Provide[PackageSourceFacts](s, PackageSourceFacts{ReadOn: a.packageSourcesOn}); e != nil {
		return e
	}
	return module.Provide[ClientWorkAccess](s, a)
}
func (a clientWork) LockProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	if strings.HasSuffix(permission, "_read") || permission == "work_item_assign" || permission == "exception_decide_replay" {
		// The caller has already locked/checked the current Human and tenant
		// membership. Read scope is checked with the requested permission;
		// terminal projects retain their immutable collaboration history.
		if tx == nil || tx.Statement == nil {
			return ErrInvalidArgument
		}
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			return ErrInvalidArgument
		}
		actor := authn.PrincipalIDFromContext(ctx)
		if a.m.workPolicy == nil {
			return ErrUnavailable
		}
		c, e := a.m.workPolicy(ctx, tx, tenant, project, strings.TrimSuffix(permission, "_replay"), actor)
		if e != nil {
			return e
		}
		if !c.AllowsEntityRows(project) {
			return ErrWorkForbidden
		}
		q := query.BindProjects(tx, a.m.service.names)
		// Assignment owner scope is applied by DispatchAccess to Work.CreatedBy.
		// This preliminary project fence must not filter Project.CreatedBy.
		projectConstraint := c
		if permission == "work_item_assign" && len(c.OwnerIDs) > 0 {
			projectConstraint = authz.DataConstraint{ResourceIDs: []string{project.String()}}
		}
		scope, e := workScope(projectConstraint, project, q.CreatedBy)
		if e != nil {
			return e
		}
		stmt := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project))
		if !strings.HasSuffix(permission, "_read") {
			stmt = stmt.Where(q.ArchivedAt.IsNull())
		}
		if scope != nil {
			stmt = stmt.Where(scope)
		}
		_, e = stmt.First()
		return e
	}
	_, kind, e := a.m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), permission)
	if e != nil {
		return e
	}
	if kind != "HUMAN" {
		return ErrInvalidMember
	}
	return nil
}
func (a clientWork) RequirementsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ids []guid.ID) ([]ClientRequirement, error) {
	if len(ids) < 1 || len(ids) > 50 {
		return nil, ErrInvalidArgument
	}
	out := make([]ClientRequirement, 0, len(ids))
	seen := map[guid.ID]bool{}
	r := query.BindRequirements(tx, a.m.service.names)
	c := query.BindCriteria(tx, a.m.service.names)
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, ErrInvalidArgument
		}
		seen[id] = true
		row, e := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ID.Eq(id), r.ArchivedAt.IsNull()).First()
		if e != nil {
			return nil, e
		}
		criteria, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.RequirementID.Eq(id), c.ArchivedAt.IsNull(), c.RetiredInVersion.IsNull()).Order(c.Position, c.ID).Find()
		if e != nil {
			return nil, e
		}
		item := ClientRequirement{ID: id, Version: row.Version, Title: row.Title, Description: row.Description, Priority: row.Priority, Criteria: []ClientCriterion{}}
		required := false
		for _, v := range criteria {
			required = required || v.Required
			item.Criteria = append(item.Criteria, ClientCriterion{v.ID, v.Code, v.Text, v.Required, v.Position})
		}
		if !required {
			return nil, ErrInvalidArgument
		}
		out = append(out, item)
	}
	return out, nil
}

// LockRequirementReadOn applies owner scope to the target Requirement, matching
// canonical requirement reads. Project authorship cannot grant requirement scope.
func (a clientWork) LockRequirementReadOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.EntityIDFromContext(ctx) != project {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if a.m == nil || a.m.workPolicy == nil || a.m.service == nil {
		return ErrUnavailable
	}
	c, e := a.m.workPolicy(ctx, tx, tenant, project, "requirement_read", authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return e
	}
	r := query.BindRequirements(tx, a.m.service.names)
	scope, e := workScope(c, project, r.CreatedBy)
	if e != nil {
		return e
	}
	p := query.BindProjects(tx, a.m.service.names)
	if _, e = p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).First(); e != nil {
		return e
	}
	stmt := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ID.Eq(id), r.ArchivedAt.IsNull())
	if scope != nil {
		stmt = stmt.Where(scope)
	}
	_, e = stmt.First()
	return e
}

// SignRequirementsOn consumes the frozen, human-published sheet under the
// authenticated client signature. It never changes already-baselined content.
func (a clientWork) SignRequirementsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, items []ClientRequirement) error {
	ids := make([]guid.ID, len(items))
	for i, v := range items {
		ids[i] = v.ID
	}
	current, e := a.RequirementsOn(ctx, tx, tenant, project, ids)
	if e != nil {
		return e
	}
	before, _ := json.Marshal(items)
	after, _ := json.Marshal(current)
	if string(before) != string(after) {
		return ErrRequirementVersion
	}
	actor := authn.PrincipalIDFromContext(ctx)
	r := query.BindRequirements(tx, a.m.service.names)
	c := query.BindCriteria(tx, a.m.service.names)
	for _, item := range items {
		row, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ID.Eq(item.ID)).First()
		if e != nil {
			return e
		}
		if row.Status == "BASELINED" {
			continue
		}
		if row.Status != "DRAFT" || row.CurrentVersionNo != 0 {
			return ErrRequirementFrozen
		}
		criteria := make([]requirementCriterionView, len(item.Criteria))
		for i, v := range item.Criteria {
			criteria[i] = requirementCriterionView{ID: v.ID, Code: v.Code, Text: v.Text, Required: v.Required, Position: v.Position, IntroducedInVersion: 1}
		}
		snapshot := requirementSnapshot{Title: item.Title, Description: item.Description, Priority: item.Priority, Criteria: criteria}
		encoded, e := json.Marshal(snapshot)
		if e != nil {
			return e
		}
		digest := sha256.Sum256(encoded)
		data, e := json.Marshal(criteria)
		if e != nil {
			return e
		}
		id, e := a.m.ids.Next()
		if e != nil {
			return e
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		v := &model.RequirementVersion{TenantID: tenant, ID: guid.ID(id), ProjectID: project, RequirementID: item.ID, VersionNo: 1, Title: item.Title, Description: item.Description, Priority: item.Priority, Criteria: string(data), ContentHash: hex.EncodeToString(digest[:]), CreatedAt: now, CreatedBy: &actor}
		if e = query.BindRequirementVersions(tx, a.m.service.names).WithContext(ctx).Create(v); e != nil {
			return e
		}
		if _, e = c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.RequirementID.Eq(item.ID), c.ArchivedAt.IsNull(), c.RetiredInVersion.IsNull()).UpdateSimple(c.IntroducedInVersion.Value(1)); e != nil {
			return e
		}
		changed, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ID.Eq(item.ID), r.Version.Eq(item.Version)).UpdateSimple(r.Status.Value("BASELINED"), r.CurrentVersionNo.Value(1), r.Version.Add(1), r.UpdatedAt.Value(now), r.UpdatedBy.Value(actor))
		if e != nil {
			return e
		}
		if changed.RowsAffected != 1 {
			return ErrRequirementVersion
		}
		row.Status = "BASELINED"
		row.CurrentVersionNo = 1
		row.Version++
		if e = a.clientBaselineEventOn(ctx, tx, row, actor, v.ContentHash); e != nil {
			return e
		}
	}
	return nil
}

// ClientArtifact deliberately omits execution, cost, work IDs and internal QA
// threads. Approved override risks remain explicit in the published manifest.
type ClientArtifactRef struct {
	WorkItemID guid.ID `json:"work_item_id"`
	ResultID   guid.ID `json:"result_id"`
	EvidenceID guid.ID `json:"evidence_id"`
}

// ClientSourceRef identifies actual file bytes before a WorkResult exists.
// It makes no execution, verification, coverage or acceptance assertion.
type ClientSourceRef struct {
	WorkItemID guid.ID `json:"work_item_id"`
	FileID     guid.ID `json:"file_id"`
	VersionID  guid.ID `json:"version_id"`
	SHA256     string  `json:"sha256" pattern:"^[a-f0-9]{64}$"`
}
type ClientArtifact struct {
	Name      string          `json:"name"`
	VersionID guid.ID         `json:"version_id"`
	SHA256    string          `json:"sha256"`
	Coverage  string          `json:"coverage"`
	Overrides json.RawMessage `json:"verification_overrides"`
}

func (a clientWork) ValidateWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, defect bool) error {
	w, e := a.m.readWork(ctx, tx, tenant, project, id, authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return e
	}
	if defect && w.Type != "BUG" {
		return ErrInvalidArgument
	}
	return nil
}
func (a clientWork) AcceptedArtifactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []ClientArtifactRef) ([]ClientArtifact, error) {
	return a.clientArtifactsOn(ctx, tx, tenant, project, refs, true)
}

func (a clientWork) clientArtifactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []ClientArtifactRef, accepted bool) ([]ClientArtifact, error) {
	if len(refs) == 0 || len(refs) > 100 {
		return nil, ErrInvalidArgument
	}
	out := make([]ClientArtifact, 0, len(refs))
	seen := map[guid.ID]bool{}
	for _, ref := range refs {
		if ref.EvidenceID <= 0 || seen[ref.EvidenceID] {
			return nil, ErrInvalidArgument
		}
		seen[ref.EvidenceID] = true
		w := query.BindWorkItems(tx, a.m.service.names)
		work, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(ref.WorkItemID), w.ArchivedAt.IsNull()).First()
		if e != nil {
			return nil, e
		}
		if work.BaselineStale || (accepted && (work.State != "ACCEPTED" || work.AcceptedAt == nil || work.AcceptedDecisionID == nil || work.CurrentClaimID == nil || *work.CurrentClaimID != ref.ResultID)) {
			return nil, ErrResultState
		}
		row, e := a.m.resultOn(ctx, tx, tenant, project, work.ID, ref.ResultID)
		if e != nil {
			return nil, e
		}
		if accepted {
			if e = a.m.resultAcceptanceReadyOn(ctx, tx, work, row); e != nil {
				return nil, e
			}
		} else if row.BaselineVersionNo != work.BaselineVersionNo {
			return nil, ErrResultState
		}
		evidence := query.BindResultEvidence(tx, a.m.service.names)
		item, e := evidence.WithContext(ctx).Where(evidence.TenantID.Eq(tenant), evidence.ProjectID.Eq(project), evidence.WorkItemID.Eq(work.ID), evidence.ResultID.Eq(row.ID), evidence.ID.Eq(ref.EvidenceID)).First()
		if e != nil {
			return nil, e
		}
		if item.Coverage != "COVERED" || len(item.SHA256) != 64 {
			return nil, ErrResultCoverage
		}
		if a.m.resultFiles == nil {
			return nil, ErrUnavailable
		}
		file, e := a.m.resultFiles.InspectOn(ctx, tx, item.FileID)
		if e != nil {
			return nil, e
		}
		if file.VersionID != item.VersionID || file.SHA256 != item.SHA256 {
			return nil, ErrResultCoverage
		}
		view, e := a.m.resultViewOn(ctx, tx, row)
		if e != nil {
			return nil, e
		}
		actorLabel, e := a.m.packageActorLabelOn(ctx, tx, tenant, project)
		if e != nil {
			return nil, e
		}
		overrides, e := json.Marshal(clientVerificationOverrides(view.Overrides, view.Evidence, actorLabel))
		if e != nil {
			return nil, e
		}
		out = append(out, ClientArtifact{Name: item.Name, VersionID: item.VersionID, SHA256: item.SHA256, Coverage: item.Coverage, Overrides: overrides})
	}
	return out, nil
}

type ClientSettings struct {
	Contacts       []projectContactInput      `json:"contacts"`
	Repositories   []ProjectRepositorySetting `json:"repositories"`
	Members        []projectRoleBindingView   `json:"members"`
	LeadAgentID    *guid.ID                   `json:"lead_agent_id"`
	DispatchPolicy string                     `json:"dispatch_policy"`
	PortalSettings json.RawMessage            `json:"portal_settings"`
	Name           string                     `json:"name"`
	Description    *string                    `json:"description"`
	TargetDate     *string                    `json:"target_date"`
	Version        int                        `json:"version"`
}
type ClientSettingsPatch struct {
	Contacts        *[]projectContactInput      `json:"contacts,omitempty" maxItems:"20"`
	Repositories    *[]ProjectRepositorySetting `json:"repositories,omitempty" maxItems:"10"`
	AddMembers      []ProjectMemberSetting      `json:"add_members,omitempty" maxItems:"50"`
	RemoveMembers   []ProjectMemberSetting      `json:"remove_members,omitempty" maxItems:"50"`
	LeadPrincipalID *guid.ID                    `json:"lead_principal_id,omitempty" doc:"Current project LEAD Agent principal; zero clears outside AUTONOMOUS mode."`
	DispatchPolicy  *string                     `json:"dispatch_policy,omitempty" enum:"MANUAL,AUTO"`
	PortalSettings  *projectPortalInput         `json:"portal_settings,omitempty"`
	Name            *string                     `json:"name,omitempty" minLength:"1" maxLength:"200"`
	Description     *string                     `json:"description,omitempty" maxLength:"20000"`
	TargetDate      *string                     `json:"target_date,omitempty" doc:"YYYY-MM-DD; empty clears the date."`
}

func (a clientWork) SettingsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in *ClientSettingsPatch, expected int) (ClientSettings, error) {
	row, kind, e := a.m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "project_manage")
	if e != nil {
		return ClientSettings{}, e
	}
	if kind != "HUMAN" {
		return ClientSettings{}, ErrInvalidMember
	}
	if a.m.service.entities.GetOn == nil {
		return ClientSettings{}, ErrUnavailable
	}
	entity, e := a.m.service.entities.GetOn(ctx, tx, tenant, project)
	if e != nil {
		return ClientSettings{}, e
	}
	if in != nil {
		if row.Version != expected {
			return ClientSettings{}, ErrWorkVersion
		}
		if in.Name == nil && in.Description == nil && in.TargetDate == nil && in.Contacts == nil && in.Repositories == nil && len(in.AddMembers) == 0 && len(in.RemoveMembers) == 0 && in.LeadPrincipalID == nil && in.DispatchPolicy == nil && in.PortalSettings == nil {
			return ClientSettings{}, ErrInvalidArgument
		}
		if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len(strings.TrimSpace(*in.Name)) > 200) {
			return ClientSettings{}, ErrInvalidArgument
		}
		if in.Description != nil {
			if len(*in.Description) > 20000 {
				return ClientSettings{}, ErrInvalidArgument
			}
			row.Description = in.Description
		}
		if in.TargetDate != nil {
			if *in.TargetDate == "" {
				row.TargetDate = nil
			} else {
				if _, e = time.Parse("2006-01-02", *in.TargetDate); e != nil {
					return ClientSettings{}, ErrInvalidArgument
				}
				row.TargetDate = in.TargetDate
			}
		}
		if in.Name != nil {
			if a.m.service.entities.UpdateOn == nil {
				return ClientSettings{}, ErrUnavailable
			}
			entity, e = a.m.service.entities.UpdateOn(ctx, tx, tenant, project, core.EntityPatch{Name: in.Name})
			if e != nil {
				return ClientSettings{}, e
			}
		}
		if e = a.m.editSettingsDetailsOn(ctx, tx, row, in); e != nil {
			return ClientSettings{}, e
		}
		row.Version++
		row.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		actor := authn.PrincipalIDFromContext(ctx)
		row.UpdatedBy = &actor
		q := query.BindProjects(tx, a.m.service.names)
		if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Select(q.Description, q.TargetDate, q.LeadAgentID, q.DispatchPolicy, q.DispatchPolicyExplicit, q.PortalSettings, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(row); e != nil {
			return ClientSettings{}, e
		}
	}
	out := ClientSettings{Name: entity.Name, Description: row.Description, TargetDate: row.TargetDate, Version: row.Version, LeadAgentID: row.LeadAgentID, DispatchPolicy: row.DispatchPolicy, PortalSettings: json.RawMessage(row.PortalSettings)}
	if e = a.m.settingsDetailsOn(ctx, tx, tenant, project, &out); e != nil {
		return ClientSettings{}, e
	}
	return out, nil
}

func (a clientWork) ArtifactFileOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ref ClientArtifactRef) (files.PinnedCandidate, error) {
	if _, e := a.AcceptedArtifactsOn(ctx, tx, tenant, project, []ClientArtifactRef{ref}); e != nil {
		return files.PinnedCandidate{}, e
	}
	q := query.BindResultEvidence(tx, a.m.service.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(ref.WorkItemID), q.ResultID.Eq(ref.ResultID), q.ID.Eq(ref.EvidenceID)).First()
	if e != nil {
		return files.PinnedCandidate{}, e
	}
	return a.m.resultFiles.InspectOn(ctx, tx, row.FileID)
}

type ClientProjectSummary struct {
	ID       guid.ID `json:"id"`
	TenantID guid.ID `json:"tenant_id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Status   string  `json:"status"`
}

func (a clientWork) ClientSummaryOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (ClientProjectSummary, error) {
	q := query.BindProjects(tx, a.m.service.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).First()
	if e != nil {
		return ClientProjectSummary{}, e
	}
	return ClientProjectSummary{ID: project, TenantID: tenant, Name: r.Code, Type: "project", Status: "active"}, nil
}

func (a clientWork) clientBaselineEventOn(ctx context.Context, tx *gorm.DB, row *model.Requirement, actor guid.ID, hash string) error {
	e := audit.NewDomainEvent(ctx, row.TenantID, "delivery.client_requirement_signed", "requirement", row.ID)
	e.Detail["project_id"] = row.ProjectID.String()
	e.Detail["version_no"] = row.CurrentVersionNo
	e.Detail["content_hash"] = hash
	e.Detail["authority_source"] = "permission:channel_read"
	if err := a.m.service.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := a.m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "requirement.versioned", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:channel_read"}, Subject: events.Subject{Kind: "requirement", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"number": row.Number, "version_no": row.CurrentVersionNo, "content_hash": hash}})
	return err
}

// Client override facts preserve original verdicts and risks without internal IDs.
type ClientVerificationOverride struct {
	Actor              ClientOverrideActor      `json:"actor"`
	SupportingEvidence []ClientOverrideEvidence `json:"supporting_evidence"`
	Reason             string                   `json:"reason"`
	Risk               string                   `json:"risk"`
	Original           []ClientOriginalVerdict  `json:"original_verdicts"`
	CreatedAt          time.Time                `json:"created_at"`
}
type ClientOriginalVerdict struct {
	Verdict     string `json:"verdict"`
	ReceiptHash string `json:"receipt_hash"`
}

type ClientOverrideActor struct {
	Kind    string `json:"kind"`
	Display string `json:"display"`
}
type ClientOverrideEvidence struct {
	Name      string  `json:"name"`
	VersionID guid.ID `json:"version_id"`
	SHA256    string  `json:"sha256"`
}

func clientVerificationOverrides(in []verificationOverrideView, evidence []resultEvidenceView, actorDisplay ...string) []ClientVerificationOverride {
	label := "TEAM"
	if len(actorDisplay) > 0 && actorDisplay[0] == "ENGINEER" {
		label = "ENGINEER"
	}
	out := make([]ClientVerificationOverride, 0, len(in))
	for _, v := range in {
		original := make([]ClientOriginalVerdict, 0, len(v.Original))
		for _, r := range v.Original {
			original = append(original, ClientOriginalVerdict{r.Verdict, r.ReceiptHash})
		}
		support := []ClientOverrideEvidence{}
		for _, id := range v.EvidenceIDs {
			for _, e := range evidence {
				if e.ID == id {
					support = append(support, ClientOverrideEvidence{e.Name, e.VersionID, e.SHA256})
				}
			}
		}
		out = append(out, ClientVerificationOverride{Actor: ClientOverrideActor{Kind: "HUMAN", Display: label}, SupportingEvidence: support, Reason: v.Reason, Risk: v.Risk, Original: original, CreatedAt: v.CreatedAt})
	}
	return out
}

// IntakeAppender delegates an Intake append to the canonical scope command. A
// genuine unconverted CHANGE source is required; the owning orchestration checks
// its frozen card and client decision in the same transaction.
type IntakeAppender struct {
	NewRequirementOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, intake.TriageDraft) (guid.ID, error)
	AppendFromIntake func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int, string, ClientScopeChange) (guid.ID, error)
}

func (m *Module) appendFromIntakeOn(ctx context.Context, tx *gorm.DB, tenant, project, requirement, source guid.ID, expected int, cookie string, in ClientScopeChange) (guid.ID, error) {
	if m.sources.GetOn == nil {
		return 0, ErrUnavailable
	}
	fact, found, e := m.sources.GetOn(ctx, tx, tenant, source)
	if e != nil {
		return 0, e
	}
	if !found || fact.Kind != "CHANGE" || fact.Status != "TRIAGED" || fact.ProjectID == nil || *fact.ProjectID != project || fact.TargetRequirementID == nil || *fact.TargetRequirementID != requirement {
		return 0, ErrInvalidArgument
	}
	out, e := m.scopeChangeOn(ctx, tx, tenant, project, requirement, authn.PrincipalIDFromContext(ctx), expected, cookie, in)
	return out.ID, e
}

// ClientScopeChange reuses the canonical scope command, including role authority,
// reauthentication, criterion diff, stale-work handling and transactional events.
type ClientScopeChange = scopeChangeBody
type ClientScopeCriteria = scopeCriteriaBody

func (a clientWork) ApplyClientChangeOn(ctx context.Context, tx *gorm.DB, tenant, project, requirement guid.ID, expected int, cookie string, in ClientScopeChange) (guid.ID, error) {
	v, e := a.m.scopeChangeOn(ctx, tx, tenant, project, requirement, authn.PrincipalIDFromContext(ctx), expected, cookie, in)
	return v.ID, e
}

type resultInput struct {
	ResultWorkInput
	ResultID string `path:"result_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) resultHTTP(ctx context.Context, in *resultInput) (*respx.Body[ResultView], error) {
	project, pe := guid.RequireID(in.EntityID)
	work, we := guid.RequireID(in.WorkItemID)
	result, re := guid.RequireID(in.ResultID)
	if pe != nil || we != nil || re != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	var out ResultView
	e := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if _, e := m.readWork(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx)); e != nil {
			return e
		}
		row, e := m.resultOn(ctx, tx, tenant, project, work, result)
		if e != nil {
			return e
		}
		out, e = m.resultViewOn(ctx, tx, row)
		return e
	})
	if e != nil {
		return nil, resultHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

// ExceptionProjectOn records the canonical project hold/cancellation transition.
// It never asserts that a running external process has stopped.
func (a clientWork) ExceptionProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, decision, reason, cookie, proof string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	row, kind, e := a.m.constitutionProjectOn(ctx, tx, tenant, project, actor, "exception_decide")
	if e != nil {
		return e
	}
	if kind != "HUMAN" || a.m.deliveryAuthority == nil {
		return ErrWorkForbidden
	}
	authority, e := a.m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: kind, Code: "exception_decide", Resource: governance.ResourceRef{Kind: "project", ID: project}})
	if e != nil {
		return e
	}
	if !authority.Allowed || authority.DelegationID != 0 || !strings.HasPrefix(authority.AuthoritySource, "ROLE:") {
		return ErrWorkForbidden
	}
	if decision != "HOLD" && decision != "KILL" {
		return nil
	}
	if decision == "KILL" {
		// KILL uses ProjectCancellationAccess after sorted participant preflight
		// and decision allocation. This legacy hold-only entry cannot skip them.
		return ErrUnavailable
	}
	var holds []map[string]json.RawMessage
	if e = json.Unmarshal([]byte(row.Holds), &holds); e != nil {
		return e
	}
	exists := false
	for _, hold := range holds {
		if string(hold["source"]) == `"TOLERANCE_EXCEPTION"` {
			exists = true
		}
	}
	if !exists {
		holds = append(holds, map[string]json.RawMessage{"kind": json.RawMessage(`"MANUAL"`), "source": json.RawMessage(`"TOLERANCE_EXCEPTION"`)})
	}
	encoded, e := json.Marshal(holds)
	if e != nil {
		return e
	}
	row.Holds = string(encoded)
	row.Version++
	row.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	row.UpdatedBy = &actor
	q := query.BindProjects(tx, a.m.service.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Select(q.Holds, q.Stage, q.StageChangedAt, q.CancelledAt, q.CancelReason, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(row); e != nil {
		return e
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.exception_project_decided", "project", project)
	event.Detail["decision"] = decision
	event.Detail["reason"] = reason
	event.Detail["authority_source"] = authority.AuthoritySource
	if e = a.m.service.audit(ctx, tx, event); e != nil {
		return e
	}

	return e
}

// ProgressArtifactOn binds genuine submitted evidence without asserting Human
// acceptance or passing verification. The caller persists this distinction.
func (a clientWork) ProgressArtifactOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ref ClientArtifactRef) (ClientArtifact, files.PinnedCandidate, error) {
	if _, e := a.m.readWork(ctx, tx, tenant, project, ref.WorkItemID, authn.PrincipalIDFromContext(ctx)); e != nil {
		return ClientArtifact{}, files.PinnedCandidate{}, e
	}
	items, e := a.clientArtifactsOn(ctx, tx, tenant, project, []ClientArtifactRef{ref}, false)
	if e != nil {
		return ClientArtifact{}, files.PinnedCandidate{}, e
	}
	q := query.BindResultEvidence(tx, a.m.service.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(ref.WorkItemID), q.ResultID.Eq(ref.ResultID), q.ID.Eq(ref.EvidenceID)).First()
	if e != nil {
		return ClientArtifact{}, files.PinnedCandidate{}, e
	}
	file, e := a.m.resultFiles.InspectOn(ctx, tx, row.FileID)
	if e != nil {
		return ClientArtifact{}, files.PinnedCandidate{}, e
	}
	return items[0], file, nil
}

// SourceArtifactOn binds an authorized project file without requiring a result.
// File inspection enforces current entity ownership and file ACL; PinOn repeats
// those checks while freezing the exact bytes in the caller's transaction.
func (a clientWork) SourceArtifactOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ref ClientSourceRef) (ClientArtifact, files.PinnedCandidate, error) {
	if tx == nil || tenant != authn.TenantIDFromContext(ctx) || project != authn.EntityIDFromContext(ctx) || ref.WorkItemID <= 0 || ref.FileID <= 0 || ref.VersionID <= 0 || len(ref.SHA256) != 64 {
		return ClientArtifact{}, files.PinnedCandidate{}, ErrInvalidArgument
	}
	if _, e := hex.DecodeString(ref.SHA256); e != nil || ref.SHA256 != strings.ToLower(ref.SHA256) {
		return ClientArtifact{}, files.PinnedCandidate{}, ErrInvalidArgument
	}
	_, e := a.m.readWork(ctx, tx, tenant, project, ref.WorkItemID, authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return ClientArtifact{}, files.PinnedCandidate{}, e
	}
	w := query.BindWorkItems(tx, a.m.service.names)
	work, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(ref.WorkItemID), w.ArchivedAt.IsNull()).First()
	if e != nil {
		return ClientArtifact{}, files.PinnedCandidate{}, e
	}
	if work.BaselineStale {
		return ClientArtifact{}, files.PinnedCandidate{}, ErrResultState
	}
	if a.m.resultFiles == nil {
		return ClientArtifact{}, files.PinnedCandidate{}, ErrUnavailable
	}
	file, e := a.m.resultFiles.InspectOn(ctx, tx, ref.FileID)
	if e != nil {
		return ClientArtifact{}, files.PinnedCandidate{}, e
	}
	if file.FileID != ref.FileID || file.VersionID != ref.VersionID || file.SHA256 != ref.SHA256 {
		return ClientArtifact{}, files.PinnedCandidate{}, ErrResultCoverage
	}
	return ClientArtifact{Name: file.Name, VersionID: file.VersionID, SHA256: file.SHA256, Coverage: "UNVERIFIED", Overrides: json.RawMessage("[]")}, file, nil
}

// ApprovePreviewShareOn uses current non-delegable Human governance authority.
func (a clientWork) ApprovePreviewShareOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	actor := authn.PrincipalIDFromContext(ctx)
	_, kind, e := a.m.constitutionProjectOn(ctx, tx, tenant, project, actor, "preview_share_approve")
	if e != nil {
		return e
	}
	if kind != "HUMAN" {
		return ErrWorkForbidden
	}
	if a.m.deliveryAuthority == nil {
		return ErrUnavailable
	}
	result, e := a.m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: kind, Code: "preview_share_approve", Resource: governance.ResourceRef{Kind: "project", ID: project}})
	if e != nil {
		return e
	}
	if !result.Allowed || result.DelegationID != 0 || !strings.HasPrefix(result.AuthoritySource, "ROLE:") {
		return ErrWorkForbidden
	}
	return nil
}

// DispatchWorkFacts contains canonical project/work state, never caller claims.
type DispatchWorkFacts struct {
	TeamMode             string   `json:"team_mode"`
	CurrentExecutionID   *guid.ID `json:"current_execution_id"`
	WorkItemID           guid.ID  `json:"work_item_id"`
	WorkVersion          int      `json:"work_version"`
	State                string   `json:"state"`
	Stage                string   `json:"stage"`
	Type                 string   `json:"type"`
	Risk                 string   `json:"risk"`
	RequiredRuntime      *string  `json:"required_runtime"`
	RequiredTags         []string `json:"required_tags"`
	PreferredTags        []string `json:"preferred_tags"`
	Policy               string   `json:"policy"`
	LeadAgentID          *guid.ID `json:"lead_agent_id"`
	BudgetRemaining      *int64   `json:"budget_remaining"`
	BudgetState          string   `json:"budget_state"`
	RequireHumanDecision bool     `json:"require_human_decision"`
	Held                 bool     `json:"held"`
}
type DispatchLoadFact struct {
	PrincipalID guid.ID `json:"principal_id"`
	ActiveItems int     `json:"active_items"`
	Member      bool    `json:"member"`
}
type DispatchAssignmentInput struct {
	EnsemblePlanID, EnsembleExecutionID, EnsembleAssignmentID guid.ID
	Auto                                                      bool
	WorkItemID, ProposalID, AssigneePrincipalID               guid.ID
	DecisionID                                                guid.ID
	ExpectedWorkVersion                                       int
	Reason                                                    string
	Reassign                                                  bool
}

// DispatchAccess delegates the canonical assignment mutation to its owner.
// Callers freeze recommendation inputs and recheck Fleet guards in this same tx.
type DispatchAccess struct {
	HistorySamplesOn func(context.Context, *gorm.DB, guid.ID, string, time.Time) (int64, error)
	AutoLockOn       func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []guid.ID) error
	AutoWorkOn       func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (DispatchWorkFacts, error)
	AutoAuthorityOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error)
	LockStopOn       func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
	StopOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) (DispatchExecutionState, error)
	QueuesOn         func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]DispatchQueueFact, error)
	AuthorityOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error)
	WorkOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (DispatchWorkFacts, error)
	LoadsOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]DispatchLoadFact, error)
	ApplyOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, DispatchAssignmentInput) (guid.ID, error)
}

type DispatchExecutionState struct {
	ExecutionID       guid.ID    `json:"execution_id"`
	State             string     `json:"state"`
	Version           int        `json:"version"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at"`
}
type DispatchQueueFact struct {
	MachineID       guid.ID   `json:"machine_id"`
	PendingCommands int       `json:"pending_commands"`
	ObservedAt      time.Time `json:"observed_at"`
}
type DispatchExecutionAccess struct {
	PrincipalsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) ([]guid.ID, error)
	StopOn       func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) (DispatchExecutionState, error)
	QueuesOn     func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]DispatchQueueFact, error)
}
type DispatchExecutionConnector struct {
	Bind func(DispatchExecutionAccess) error
}

func (a clientWork) dispatchAutoLockOn(ctx context.Context, tx *gorm.DB, tenant, project, workID guid.ID, fleetPrincipals []guid.ID) error {
	if a.m == nil || a.m.service == nil || a.m.principals.Lock == nil {
		return ErrUnavailable
	}
	w := query.BindWorkItems(tx, a.m.service.names)
	hint, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(workID), w.ArchivedAt.IsNull()).First()
	if err != nil {
		return err
	}
	ids := append(slices.Clone(fleetPrincipals), authn.PrincipalIDFromContext(ctx), hint.AccountablePrincipalID)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if err = a.m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	_, work, err := a.dispatchAutoRowsOn(ctx, tx, tenant, project, workID)
	if err != nil {
		return err
	}
	if work.AccountablePrincipalID != hint.AccountablePrincipalID {
		return ErrWorkVersion
	}
	if row, _, e := a.dispatchAutoRowsOn(ctx, tx, tenant, project, workID); e != nil {
		return e
	} else if row.TeamMode != "HYBRID" && row.TeamMode != "AUTONOMOUS" {
		return ErrWorkForbidden
	}
	return nil
}

// dispatchHistorySamplesOn distinguishes an observed empty 90-day claim window
// from an unavailable history provider. Nonempty windows need complete decision
// analytics before AUTO can use them; acceptance-only actions are insufficient.
func (a clientWork) dispatchHistorySamplesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, since time.Time) (int64, error) {
	r := query.BindResultVersions(tx, a.m.service.names)
	w := query.BindWorkItems(tx, a.m.service.names)
	return r.WithContext(ctx).Join(w, r.TenantID.EqCol(w.TenantID), r.ProjectID.EqCol(w.ProjectID), r.WorkItemID.EqCol(w.ID)).Where(r.TenantID.Eq(tenant), w.Type.Eq(kind), r.CreatedAt.Gte(since)).Count()
}

func (a clientWork) dispatchAutoRowsOn(ctx context.Context, tx *gorm.DB, tenant, project, workID guid.ID) (*model.Project, *model.WorkItem, error) {
	row, kind, err := a.m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "work_item_read")
	if err != nil {
		return nil, nil, err
	}
	if kind != "AGENT" || a.m.agents.ByPrincipalOn == nil {
		return nil, nil, ErrWorkForbidden
	}
	agent, found, err := a.m.agents.ByPrincipalOn(ctx, tx, tenant, authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return nil, nil, err
	}
	if !found || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "LEAD") || row.LeadAgentID == nil || *row.LeadAgentID != agent.ID || row.DispatchPolicy != "AUTO" {
		return nil, nil, ErrWorkForbidden
	}
	w := query.BindWorkItems(tx, a.m.service.names)
	work, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(workID), w.ArchivedAt.IsNull()).First()
	if err != nil {
		return nil, nil, err
	}
	return row, work, nil
}

func (a *clientWork) dispatchAutoAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, assignee guid.ID) (string, error) {
	if a.autoAuthority == nil {
		return "", ErrUnavailable
	}
	projectRow, work, err := a.dispatchAutoRowsOn(ctx, tx, tenant, project, workID)
	if err != nil {
		return "", err
	}
	facts, err := a.dispatchAutoWorkOn(ctx, tx, tenant, project, workID)
	if err != nil {
		return "", err
	}
	var holds []struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(projectRow.Holds), &holds) != nil {
		return "", ErrUnavailable
	}
	if slices.ContainsFunc(holds, func(h struct {
		Kind string `json:"kind"`
	}) bool {
		return h.Kind == autonomyHold
	}) {
		return "", ErrWorkForbidden
	}
	if facts.BudgetRemaining != nil || facts.BudgetState == "EXHAUSTED" {
		return "", ErrWorkForbidden
	}
	result, err := a.autoAuthority(ctx, tx, governance.DispatchAutoFacts{TenantID: tenant, ProjectID: project, WorkItemID: workID, PrincipalID: authn.PrincipalIDFromContext(ctx), AgentID: *projectRow.LeadAgentID, LeadAgentID: *projectRow.LeadAgentID, ActiveLead: true, Policy: projectRow.DispatchPolicy, TeamMode: projectRow.TeamMode, Stage: projectRow.Stage, Risk: work.Risk, WorkType: work.Type, Held: facts.Held, RequireHumanDecision: work.RequireHumanDecision, AssigneePrincipalID: assignee})
	if err != nil {
		return "", err
	}
	if !result.Allowed || result.AuthoritySource != "POLICY:dispatch_auto" {
		return "", ErrWorkForbidden
	}
	return result.AuthoritySource, nil
}

func (a *clientWork) dispatchLockStopOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, executionID guid.ID) error {
	if a.dispatchExecution == nil || a.dispatchExecution.PrincipalsOn == nil {
		return ErrUnavailable
	}
	ids, err := a.dispatchExecution.PrincipalsOn(ctx, tx, tenant, project, workID, executionID)
	if err != nil {
		return err
	}
	ids = append(ids, authn.PrincipalIDFromContext(ctx))
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if err = a.m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	if _, err = a.dispatchWorkOn(ctx, tx, tenant, project, workID); err != nil {
		return err
	}
	current, err := a.dispatchExecution.PrincipalsOn(ctx, tx, tenant, project, workID, executionID)
	if err != nil {
		return err
	}
	for _, id := range current {
		if !slices.Contains(ids, id) {
			return ErrWorkVersion
		}
	}
	return nil
}
func (a *clientWork) dispatchStopOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, executionID guid.ID, version int) (DispatchExecutionState, error) {
	if a.dispatchExecution == nil || a.dispatchExecution.StopOn == nil {
		return DispatchExecutionState{}, ErrUnavailable
	}
	facts, err := a.dispatchWorkOn(ctx, tx, tenant, project, workID)
	if err != nil {
		return DispatchExecutionState{}, err
	}
	if facts.CurrentExecutionID == nil || *facts.CurrentExecutionID != executionID {
		return DispatchExecutionState{}, ErrWorkVersion
	}
	return a.dispatchExecution.StopOn(ctx, tx, tenant, project, workID, executionID, version)
}
func (a *clientWork) dispatchQueuesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, machines []guid.ID) ([]DispatchQueueFact, error) {
	if a.dispatchExecution == nil || a.dispatchExecution.QueuesOn == nil {
		return nil, ErrUnavailable
	}
	return a.dispatchExecution.QueuesOn(ctx, tx, tenant, machines)
}

func (a clientWork) dispatchAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, assignee guid.ID) (string, error) {
	if a.m == nil || a.m.deliveryAuthority == nil {
		return "", ErrUnavailable
	}
	if _, err := a.dispatchWorkOn(ctx, tx, tenant, project, workID); err != nil {
		return "", err
	}
	w := query.BindWorkItems(tx, a.m.service.names)
	work, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(workID)).First()
	if err != nil {
		return "", err
	}
	result, err := a.m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: authn.PrincipalIDFromContext(ctx), ActorKind: "HUMAN", Code: "work_item_assign", Resource: governance.ResourceRef{Kind: "work_item", ID: workID}, Facts: &governance.WorkItemFacts{Type: work.Type, Risk: work.Risk, AssignmentRole: "EXECUTOR", AssigneePrincipalID: assignee, RequireHumanDecision: work.RequireHumanDecision, Paused: work.PausedAt != nil}})
	if err != nil {
		return "", err
	}
	if !result.Allowed || result.AuthoritySource == "" {
		return "", ErrWorkForbidden
	}
	return result.AuthoritySource, nil
}

func (a clientWork) dispatchWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (DispatchWorkFacts, error) {
	return a.dispatchWorkModeOn(ctx, tx, tenant, project, id, false)
}
func (a clientWork) dispatchAutoWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (DispatchWorkFacts, error) {
	return a.dispatchWorkModeOn(ctx, tx, tenant, project, id, true)
}
func (a clientWork) dispatchWorkModeOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, auto bool) (DispatchWorkFacts, error) {
	var out DispatchWorkFacts
	// Resolve only the owner hint first. The canonical lock path rechecks the
	// owner and applies assignment scope to the work, not the project creator.
	if tx == nil || a.m == nil || a.m.service == nil {
		return out, ErrUnavailable
	}
	w := query.BindWorkItems(tx, a.m.service.names)
	hint, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(id), w.ArchivedAt.IsNull()).First()
	if e != nil {
		return out, e
	}
	var row *model.Project
	var work *model.WorkItem
	if auto {
		row, work, e = a.dispatchAutoRowsOn(ctx, tx, tenant, project, id)
	} else {
		row, work, _, e = a.m.lockedWorkOn(ctx, tx, tenant, project, id, authn.PrincipalIDFromContext(ctx), hint.AccountablePrincipalID, "work_item_assign")
	}
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal([]byte(work.SkillTags), &out.RequiredTags); e != nil {
		return out, e
	}
	if e = json.Unmarshal([]byte(work.PreferredSkillTags), &out.PreferredTags); e != nil {
		return out, e
	}
	out.WorkItemID = id
	out.CurrentExecutionID = work.CurrentExecutionID
	out.WorkVersion = work.Version
	out.State = work.State
	out.Stage = row.Stage
	out.Type = work.Type
	out.Risk = work.Risk
	out.RequiredRuntime = work.RequiredRuntime
	out.Policy = row.DispatchPolicy
	out.TeamMode = row.TeamMode
	out.LeadAgentID = row.LeadAgentID
	out.BudgetState = row.BudgetState
	out.RequireHumanDecision = work.RequireHumanDecision
	out.Held = work.PausedAt != nil || row.BlockedKind != nil
	var holds []struct {
		Kind string `json:"kind"`
	}
	if e = json.Unmarshal([]byte(row.Holds), &holds); e != nil {
		return out, e
	}
	for _, h := range holds {
		if slices.Contains([]string{"BUDGET", "MANUAL", "ESCALATION"}, h.Kind) {
			out.Held = true
		}
	}
	if row.BudgetCapMicros != nil {
		remaining := max(int64(0), *row.BudgetCapMicros-row.BudgetSpentMicros)
		out.BudgetRemaining = &remaining
	}
	return out, nil
}
func (a clientWork) dispatchLoadsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, principals []guid.ID) ([]DispatchLoadFact, error) {
	if len(principals) > 100 {
		return nil, ErrUnavailable
	}
	ids := slices.Clone(principals)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := make([]DispatchLoadFact, 0, len(ids))
	q := query.BindAssignments(tx, a.m.service.names)
	for _, id := range ids {
		if e := a.m.principals.Lock(ctx, tx, id); e != nil {
			return nil, e
		}
		member, e := a.m.members.IsMemberActiveOn(ctx, tx, tenant, id)
		if e != nil {
			return nil, e
		}
		if member {
			c, e := a.m.workPolicy(ctx, tx, tenant, project, "work_item_execute", id)
			if e != nil {
				if errors.Is(e, ErrWorkForbidden) {
					member = false
				} else {
					return nil, e
				}
			} else {
				member = c.AllowAll || slices.Contains(c.ResourceIDs, project.String())
			}
		}
		n, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AssigneePrincipalID.Eq(id), q.Role.Eq("EXECUTOR"), q.Status.Eq("ACTIVE")).Count()
		if e != nil {
			return nil, e
		}
		out = append(out, DispatchLoadFact{PrincipalID: id, ActiveItems: int(n), Member: member})
	}
	return out, nil
}
func (a clientWork) dispatchApplyOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in DispatchAssignmentInput) (guid.ID, error) {
	return a.dispatchApplyActorOn(ctx, ctx, tx, tenant, project, in, nil)
}

func (a clientWork) dispatchApplyActorOn(ctx, writeCtx context.Context, tx *gorm.DB, tenant, project guid.ID, in DispatchAssignmentInput, automation *AutomationAssignment) (guid.ID, error) {
	heavy := in.EnsemblePlanID > 0
	if heavy {
		if a.m.ensembleBranchCheck == nil {
			return 0, ErrUnavailable
		}
		if e := a.m.ensembleBranchCheck(ctx, tx, tenant, project, in.WorkItemID, in.EnsemblePlanID, in.EnsembleAssignmentID, in.EnsembleExecutionID); e != nil {
			return 0, e
		}
	}
	if (!heavy && automation == nil && in.ProposalID <= 0) || in.AssigneePrincipalID <= 0 || strings.TrimSpace(in.Reason) == "" {
		return 0, ErrInvalidArgument
	}
	workOn := a.dispatchWorkOn
	if in.Auto {
		workOn = a.dispatchAutoWorkOn
	}
	facts, e := workOn(ctx, tx, tenant, project, in.WorkItemID)
	if e != nil {
		return 0, e
	}
	if facts.WorkVersion != in.ExpectedWorkVersion {
		return 0, ErrWorkVersion
	}
	if facts.Stage != "ACTIVE" || facts.Held || facts.BudgetState == "EXHAUSTED" {
		return 0, ErrWorkProjectReadOnly
	}
	w := query.BindWorkItems(tx, a.m.service.names)
	work, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(in.WorkItemID)).First()
	if e != nil {
		return 0, e
	}
	if work.IsContainer || work.BaselineStale || work.CurrentClaimID != nil {
		return 0, ErrAssignmentState
	}
	if work.CurrentExecutionID != nil {
		return 0, ErrUnavailable
	}
	loads, e := a.dispatchLoadsOn(ctx, tx, tenant, project, []guid.ID{in.AssigneePrincipalID})
	if e != nil {
		return 0, e
	}
	if len(loads) != 1 || !loads[0].Member {
		return 0, ErrInvalidMember
	}
	if a.m.agents.ByPrincipalOn == nil {
		return 0, ErrUnavailable
	}
	identity, agent, e := a.m.agents.ByPrincipalOn(ctx, tx, tenant, in.AssigneePrincipalID)
	if e != nil {
		return 0, e
	}
	if in.Auto && !agent {
		return 0, ErrInvalidMember
	}
	kind := "HUMAN"
	var agentID *guid.ID
	ttl := int64(14400000)
	if agent {
		if identity.State != "ACTIVE" || !slices.Contains(identity.Roles, "EXECUTOR") {
			return 0, ErrInvalidMember
		}
		kind = "AGENT"
		agentID = &identity.ID
		ttl = 600000
	} else {
		human, active, e := a.m.principals.ActiveHumanByID(ctx, tx, in.AssigneePrincipalID)
		if e != nil {
			return 0, e
		}
		if !active || human.ID != in.AssigneePrincipalID {
			return 0, ErrInvalidMember
		}
	}
	q := query.BindAssignments(tx, a.m.service.names)
	current, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.WorkItemID.Eq(work.ID), q.Role.Eq("EXECUTOR"), q.Status.Eq("ACTIVE"), q.EnsemblePlanID.IsNull()).First()
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return 0, e
	}
	if current != nil && !in.Reassign {
		return 0, ErrAssignmentHeld
	}
	if in.Reassign {
		if current == nil || current.AssigneePrincipalID == in.AssigneePrincipalID || !slices.Contains([]string{"CLAIMED", "IN_PROGRESS", "EXECUTION_FAILED"}, work.State) {
			return 0, ErrAssignmentState
		}
	} else if work.State != "READY" && !(heavy && work.State == "CLAIMED") {
		return 0, ErrAssignmentState
	}
	if e = a.m.principals.Lock(ctx, tx, work.AccountablePrincipalID); e != nil {
		return 0, e
	}
	ownerMember, e := a.m.members.IsMemberActiveOn(ctx, tx, tenant, work.AccountablePrincipalID)
	if e != nil {
		return 0, e
	}
	owner, active, e := a.m.principals.ActiveHumanByID(ctx, tx, work.AccountablePrincipalID)
	if e != nil {
		return 0, e
	}
	issues, blocks, e := a.m.evaluatePreparationOn(ctx, tx, work, ownerMember && active && owner.ID == work.AccountablePrincipalID)
	if e != nil {
		return 0, e
	}
	if len(issues) > 0 || len(blocks) > 0 || !work.ReadinessOK {
		return 0, ErrAssignmentReadiness
	}
	if a.m.deliveryAuthority == nil && !in.Auto {
		return 0, ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	actorKind := "HUMAN"
	var authority governance.AuthorityDecision
	if in.Auto {
		actorKind = "AGENT"
		var source string
		source, e = a.dispatchAutoAuthorityOn(ctx, tx, tenant, project, work.ID, in.AssigneePrincipalID)
		authority = governance.AuthorityDecision{Allowed: e == nil, AuthoritySource: source}
	} else {
		authority, e = a.m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, ActorKind: "HUMAN", PrincipalID: actor, Code: "work_item_assign", Resource: governance.ResourceRef{Kind: "work_item", ID: work.ID}, Facts: &governance.WorkItemFacts{Type: work.Type, Risk: work.Risk, AssignmentRole: "EXECUTOR", AssigneePrincipalID: in.AssigneePrincipalID, RequireHumanDecision: work.RequireHumanDecision, Paused: work.PausedAt != nil}})
	}
	if e != nil {
		return 0, e
	}
	if !authority.Allowed || authority.AuthoritySource == "" {
		return 0, ErrWorkForbidden
	}
	writer := &actor
	if automation != nil {
		actor, actorKind, writer = 0, "SYSTEM", nil
		authority.AuthoritySource = automation.RunAs
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	if current != nil {
		reason := "DISPATCH_REASSIGNED"
		current.Status = "RELEASED"
		current.ActiveSlot = nil
		current.EndReason = &reason
		current.EndedAt = &at
		current.LeaseExpiresAt = nil
		current.UpdatedAt = at
		current.UpdatedBy = writer
		current.Version++
		if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(current.ID)).Select(q.Status, q.ActiveSlot, q.EndReason, q.EndedAt, q.LeaseExpiresAt, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(current); e != nil {
			return 0, e
		}
	}
	value, e := a.m.ids.Next()
	if e != nil {
		return 0, e
	}
	id := guid.ID(value)
	if heavy {
		id = in.EnsembleAssignmentID
	}
	expires := at.Add(time.Duration(ttl) * time.Millisecond)
	assignment := &model.Assignment{TenantID: tenant, ID: id, ProjectID: project, WorkItemID: work.ID, Role: "EXECUTOR", AssigneeKind: kind, AssigneePrincipalID: in.AssigneePrincipalID, AgentID: agentID, ActiveSlot: &work.ID, Status: "ACTIVE", LeaseTTLMS: ttl, LeaseExpiresAt: &expires, Version: 1, CreatedAt: at, UpdatedAt: at, CreatedBy: writer, UpdatedBy: writer}
	if heavy {
		assignment.EnsemblePlanID = &in.EnsemblePlanID
		assignment.ActiveSlot = nil
	}
	if e = q.WithContext(ctx).Create(assignment); e != nil {
		return 0, e
	}
	before := work.State
	expected := work.Version
	work.State = "CLAIMED"
	work.Version++
	work.StateChangedAt = at
	work.UpdatedAt = at
	work.UpdatedBy = writer
	if work.FirstClaimedAt == nil {
		work.FirstClaimedAt = &at
	}
	changed, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(work.ID), w.Version.Eq(expected)).Select(w.State, w.Version, w.StateChangedAt, w.UpdatedAt, w.UpdatedBy, w.FirstClaimedAt).Updates(work)
	if e != nil {
		return 0, e
	}
	if changed.RowsAffected != 1 {
		return 0, ErrWorkVersion
	}
	transitionID, e := a.m.ids.Next()
	if e != nil {
		return 0, e
	}
	reason := "DISPATCH_ASSIGNED"
	if in.Reassign {
		reason = "DISPATCH_REASSIGNED"
	}
	source := authority.AuthoritySource
	transition := &model.WorkItemTransition{TenantID: tenant, ID: guid.ID(transitionID), ProjectID: project, WorkItemID: work.ID, FromState: &before, ToState: "CLAIMED", Trigger: "COMMAND", ActorKind: actorKind, ActorID: writer, AuthoritySource: source, ReasonCode: reason, VersionAfter: work.Version, CreatedAt: at}
	if automation != nil {
		transition.Trigger = "SYSTEM"
		ref := automation.RunAs + ":run:" + automation.RunID.String()
		transition.SourceRef = &ref
	}
	if in.DecisionID > 0 {
		transition.DecisionID = &in.DecisionID
		transition.Trigger = "DECISION"
	}
	if e = query.BindWorkTransitions(tx, a.m.service.names).WithContext(ctx).Create(transition); e != nil {
		return 0, e
	}
	event := audit.NewDomainEvent(writeCtx, tenant, "delivery.work_assignment_changed", "work_item", work.ID)
	event.Detail["dispatch_proposal_id"] = in.ProposalID.String()
	if heavy {
		delete(event.Detail, "dispatch_proposal_id")
		event.Detail["ensemble_plan_id"] = in.EnsemblePlanID.String()
	}
	if in.DecisionID > 0 {
		event.Detail["decision_id"] = in.DecisionID.String()
	}
	event.Detail["assignment_id"] = id.String()
	event.Detail["assignee_kind"] = kind
	event.Detail["assignee_principal_id"] = in.AssigneePrincipalID.String()
	event.Detail["reason"] = in.Reason
	event.Detail["authority_source"] = source
	if automation != nil {
		if in.ProposalID == 0 {
			delete(event.Detail, "dispatch_proposal_id")
		}
		event.Detail["actor_kind"] = "SYSTEM"
		event.Detail["rule_id"] = automation.RuleID
		event.Detail["run_id"] = automation.RunID
		event.Detail["creator_id"] = automation.CreatorID
	}
	if e = a.m.service.audit(writeCtx, tx, event); e != nil {
		return 0, e
	}
	changedData := map[string]any{"from": before, "to": "CLAIMED", "reason_code": reason, "assignment_id": id.String(), "dispatch_proposal_id": in.ProposalID.String()}
	if heavy {
		delete(changedData, "dispatch_proposal_id")
		changedData["ensemble_plan_id"] = in.EnsemblePlanID.String()
	}
	if automation != nil {
		if in.ProposalID == 0 {
			delete(changedData, "dispatch_proposal_id")
		}
		changedData["rule_id"], changedData["run_id"], changedData["creator_id"] = automation.RuleID, automation.RunID, automation.CreatorID
	}
	out := events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.state_changed", Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, SubjectVersion: int64(work.Version), Data: changedData}
	if in.DecisionID > 0 {
		changedData["decision_id"] = in.DecisionID.String()
	}
	if _, e = a.m.service.events.Publish(writeCtx, tx, out); e != nil {
		return 0, e
	}
	out.Type = "work_item.assigned"
	assignedData := map[string]any{"work_item_id": work.ID.String(), "assignment_id": id.String(), "role": "EXECUTOR", "assignee": map[string]any{"kind": kind, "principal_id": in.AssigneePrincipalID.String()}, "lease_expires_at": expires, "dispatch_proposal_id": in.ProposalID.String()}
	if heavy {
		delete(assignedData, "dispatch_proposal_id")
		assignedData["ensemble_plan_id"] = in.EnsemblePlanID.String()
	}
	if automation != nil {
		if in.ProposalID == 0 {
			delete(assignedData, "dispatch_proposal_id")
		}
		assignedData["rule_id"], assignedData["run_id"], assignedData["creator_id"] = automation.RuleID, automation.RunID, automation.CreatorID
	}
	out.Data = assignedData
	if in.DecisionID > 0 {
		assignedData["decision_id"] = in.DecisionID.String()
	}
	if _, e = a.m.service.events.Publish(writeCtx, tx, out); e != nil {
		return 0, e
	}
	if kind == "AGENT" && !heavy && requiresPlanning(work) {
		if a.m.planningRuntime.LaunchOn == nil {
			return 0, ErrUnavailable
		}
		if _, e = a.m.planningRuntime.LaunchOn(writeCtx, tx, PlanningLaunch{TenantID: tenant, ProjectID: project, WorkItemID: work.ID, AssignmentID: id, AgentPrincipalID: in.AssigneePrincipalID}); e != nil {
			return 0, e
		}
	}
	return id, nil
}

// ForecastWorkFacts is a trusted transactional measurement capability for system
// forecasting. It exposes canonical facts, never authorization or caller estimates.
type ForecastWorkSample struct {
	WorkItemID  guid.ID
	CycleMillis int64
}
type ForecastWorkMeasurement struct {
	Remaining, ScopeCriteria int64
	SpentMicros              *int64
	Samples                  []ForecastWorkSample
	AcceptedWorkIDs          []guid.ID
	WorkIDs                  []guid.ID
}
type ForecastWorkFacts struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID, time.Time) (ForecastWorkMeasurement, error)
}

// PackageSourceFacts resolves only canonical source linkage for already frozen
// package references. The caller holds current package authority/project lock.
type PackageSource struct {
	WorkItemID, RequirementID, IntakeID guid.ID
}
type PackageSourceFacts struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []ClientArtifactRef) ([]PackageSource, error)
}

func (a clientWork) packageSourcesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []ClientArtifactRef) ([]PackageSource, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || len(refs) == 0 || len(refs) > 100 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	out := make([]PackageSource, 0, len(refs))
	for _, ref := range refs {
		fact := PackageSource{WorkItemID: ref.WorkItemID}
		w := query.BindWorkItems(tx, a.m.service.names)
		work, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(ref.WorkItemID)).Take()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			out = append(out, fact)
			continue
		}
		if e != nil {
			return nil, e
		}
		if work.RequirementID != nil {
			r := query.BindRequirements(tx, a.m.service.names)
			requirement, e := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ID.Eq(*work.RequirementID)).Take()
			if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
				return nil, e
			}
			if e == nil {
				fact.RequirementID = requirement.ID
				if requirement.SourceIntakeID != nil {
					fact.IntakeID = *requirement.SourceIntakeID
				}
			}
		}
		out = append(out, fact)
	}
	return out, nil
}

func (a clientWork) forecastWorkOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, requirement *guid.ID, at time.Time) (ForecastWorkMeasurement, error) {
	var out ForecastWorkMeasurement
	if tx == nil || tenant <= 0 || project <= 0 || at.IsZero() {
		return out, ErrInvalidArgument
	}
	p := query.BindProjects(tx, a.m.service.names)
	projectRow, e := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).First()
	if e != nil {
		return out, e
	}
	if requirement == nil {
		spent := projectRow.BudgetSpentMicros
		out.SpentMicros = &spent
	}
	w := query.BindWorkItems(tx, a.m.service.names)
	rows, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ArchivedAt.IsNull(), w.IsContainer.Is(false)).Find()
	if e != nil {
		return out, e
	}
	for _, row := range rows {
		if row.State == "ACCEPTED" && row.AcceptedAt != nil && !row.AcceptedAt.Before(at.AddDate(0, 0, -30)) && !row.AcceptedAt.After(at) {
			out.AcceptedWorkIDs = append(out.AcceptedWorkIDs, row.ID)
			if row.FirstClaimedAt != nil && !row.FirstClaimedAt.After(*row.AcceptedAt) {
				out.Samples = append(out.Samples, ForecastWorkSample{WorkItemID: row.ID, CycleMillis: row.AcceptedAt.Sub(*row.FirstClaimedAt).Milliseconds()})
			}
		}
		if requirement != nil && (row.RequirementID == nil || *row.RequirementID != *requirement) {
			continue
		}
		out.WorkIDs = append(out.WorkIDs, row.ID)
		if row.State != "ACCEPTED" && row.State != "CANCELLED" { //nolint:misspell // Frozen protocol state.
			out.Remaining++
		}
	}
	r := query.BindRequirements(tx, a.m.service.names)
	scope := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ArchivedAt.IsNull())
	if requirement != nil {
		scope = scope.Where(r.ID.Eq(*requirement))
	}
	requirements, e := scope.Find()
	if e != nil {
		return out, e
	}
	if requirement != nil && len(requirements) != 1 {
		return out, ErrProjectNotFound
	}
	for _, row := range requirements {
		c := query.BindCriteria(tx, a.m.service.names)
		n, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.RequirementID.Eq(row.ID), c.ArchivedAt.IsNull(), c.IntroducedInVersion.Lte(row.CurrentVersionNo)).Where(c.RetiredInVersion.IsNull()).Count()
		if e != nil {
			return out, e
		}
		out.ScopeCriteria += n
	}
	return out, nil
}
