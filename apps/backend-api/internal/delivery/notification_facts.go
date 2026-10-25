package delivery

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DecisionNotificationFact contains identities from the actual source; event
// actors and caller-supplied audience lists never substitute for these facts.
type DecisionNotificationFact struct {
	EscalationReason                               string
	ConflictVersion                                int
	ProjectID, WorkItemID, AccountableID, AuthorID guid.ID
	State                                          string
}

type DecisionNotifications struct {
	AttentionWorksOn      func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	AcceptedUndeliveredOn func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]guid.ID, error)
	AttentionOn           func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionAttentionFact, bool, error)

	WorkReadableOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	AndonOn                func(context.Context, *gorm.DB, guid.ID, guid.ID) (AndonNotificationFact, bool, error)
	AndonEligibleOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	PublishStageReadyOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	StageProjectsOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, int) ([]guid.ID, error)
	StageSuggestionOn      func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error)
	CancelledDependentsOn  func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]DependencyNotificationFact, error)
	DeadlineOn             func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (DeadlineNotificationFact, bool, error)
	ConstitutionEligibleOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error)
	ReviewSheetOn          func(context.Context, *gorm.DB, guid.ID, guid.ID) (ReviewSheetNotificationFact, bool, error)
	ReviewSheetEligibleOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error)
	LeaseExpiredOn         func(context.Context, *gorm.DB, guid.ID, guid.ID) (LeaseExpiredNotificationFact, bool, error)
	HumanVerificationOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error)
	FactsOn                func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (DecisionNotificationFact, bool, error)
	PlanEligibleOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
}

type PendingHumanPlan struct {
	ID, ProjectID guid.ID
	CreatedAt     time.Time
}

// AndonNotificationFact identifies the persisted pull episode of a current named
// hold. An ordinary manual pause is not an Andon source.
type AndonNotificationFact struct {
	ProjectID, WorkItemID, AccountableID, OccurrenceID guid.ID
	CreatedAt                                          time.Time
}

func (m *Module) andonNotificationOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (AndonNotificationFact, bool, error) {
	var out AndonNotificationFact
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil || !andonActive(work) {
		return out, false, err
	}
	r := query.BindWorkTransitions(tx, m.service.names)
	pull, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(work.ProjectID), r.WorkItemID.Eq(id), r.ReasonCode.Eq("ANDON_PULLED"), r.CreatedAt.Eq(*work.PausedAt)).Order(r.VersionAfter.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return AndonNotificationFact{ProjectID: work.ProjectID, WorkItemID: id, AccountableID: work.AccountablePrincipalID, OccurrenceID: pull.ID, CreatedAt: pull.CreatedAt}, true, nil
}

func (m *Module) andonNotificationEligibleOn(ctx context.Context, tx *gorm.DB, tenant, id, actor guid.ID) (bool, error) {
	fact, found, err := m.andonNotificationOn(ctx, tx, tenant, id)
	if err != nil || !found || actor <= 0 {
		return false, err
	}
	if m.principals.ActiveHumanByID == nil || m.members == nil {
		return false, ErrUnavailable
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil || !active || human.ID != actor {
		return false, err
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil || !active {
		return false, err
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
	_, err = m.readWork(readCtx, tx, tenant, fact.ProjectID, id, actor)
	if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (m *Module) notificationWorkReadableOn(ctx context.Context, tx *gorm.DB, tenant, id, actor guid.ID) (bool, error) {
	f, found, err := m.decisionNotificationFactsOn(ctx, tx, tenant, id, "work_item")
	if err != nil || !found {
		return false, err
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
	_, err = m.readWork(readCtx, tx, tenant, f.ProjectID, id, actor)
	if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (m *Module) andonInbox(ctx context.Context, req governance.InboxRequest, leads governance.ProjectLead) ([]governance.InboxItem, error) {
	if req.PrincipalID <= 0 || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.TenantID != authn.TenantIDFromContext(ctx) {
		return nil, governance.ErrForbidden
	}
	if req.Kind != "ANDON" || req.ProjectID < 0 || req.Limit < 1 || req.Limit > 101 {
		return nil, governance.ErrInvalidArgument
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, governance.ErrUnavailable
	}
	out := []governance.InboxItem{}
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindWorkItems(tx, m.service.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.ArchivedAt.IsNull(), q.PausedAt.IsNotNull())
		if req.ProjectID > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(req.ProjectID))
		}
		rows, err := stmt.Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			f, found, err := m.andonNotificationOn(ctx, tx, req.TenantID, row.ID)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			yes, err := m.andonNotificationEligibleOn(ctx, tx, req.TenantID, row.ID, req.PrincipalID)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			if f.AccountableID != req.PrincipalID {
				if leads == nil {
					continue
				}
				yes, err = leads(ctx, tx, req.TenantID, f.ProjectID, req.PrincipalID)
				if err != nil {
					return err
				}
				if !yes {
					continue
				}
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "CRITICAL", CreatedAt: f.CreatedAt, Kind: "ANDON", ID: f.OccurrenceID}, ProjectID: f.ProjectID, Title: "ANDON", ObjectType: "work_item", ObjectID: f.WorkItemID, SourceScope: "PROJECT", TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
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

// DependencyNotificationFact describes a current dependent of a canceled
// predecessor. It contains no titles, dependency reasons or caller audiences.
type DependencyNotificationFact struct {
	ProjectID, WorkItemID, ExecutorID guid.ID
}

func (m *Module) stageNotificationProjectsOn(ctx context.Context, tx *gorm.DB, tenant, after guid.ID, limit int) ([]guid.ID, error) {
	if tx == nil || tenant <= 0 || after < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalidArgument
	}
	q := query.BindProjects(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Gt(after), q.ArchivedAt.IsNull(), q.Stage.In("INTAKE", "DISCOVERY", "PLANNING", "ACTIVE", "ACCEPTANCE_PENDING")).Order(q.ProjectID).Limit(limit).Find()
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ProjectID)
	}
	return ids, err
}

// stageNotificationSuggestionOn evaluates the same five forward guards as the
// canonical stage command. It changes neither the project nor its authority.
func (m *Module) stageNotificationSuggestionOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (string, error) {
	target, _, err := m.stageNotificationReadinessOn(ctx, tx, tenant, project)
	return target, err
}

func (m *Module) stageNotificationReadinessOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (string, []string, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return "", nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return "", nil, ErrInvalidArgument
	}
	candidates, err := m.stageExecutorCandidates(ctx, tx, tenant, project)
	if err != nil {
		return "", nil, err
	}
	for _, actor := range candidates {
		if err := m.principals.Lock(ctx, tx, actor); err != nil {
			return "", nil, err
		}
	}
	q := query.BindProjects(tx, m.service.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	target := map[string]string{"INTAKE": "DISCOVERY", "DISCOVERY": "PLANNING", "PLANNING": "ACTIVE", "ACTIVE": "ACCEPTANCE_PENDING", "ACCEPTANCE_PENDING": "DELIVERED"}[row.Stage]
	if target == "" {
		return "", nil, nil
	}
	eligible := false
	if row.Stage == "PLANNING" {
		eligible, err = m.stageExecutorOn(ctx, tx, tenant, project, candidates, row.TeamMode == "HUMAN_ONLY")
		if err != nil {
			return "", nil, err
		}
	}
	criteria, err := m.stageCriteriaOn(ctx, tx, row, target, eligible)
	if err != nil {
		return "", nil, err
	}
	if len(criteria) == 0 {
		return "", nil, nil
	}
	passed := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		if !criterion.Passed {
			return "", nil, nil
		}
		passed = append(passed, criterion.Key)
	}
	return target, passed, nil
}

func (m *Module) publishStageReadyOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, history events.ProjectOccurrences) error {
	// Scheduled checks receive no user identity or additional IAM grant.
	if authn.PrincipalIDFromContext(ctx) != 0 {
		return ErrWorkForbidden
	}
	target, passed, err := m.stageNotificationReadinessOn(ctx, tx, tenant, project)
	if err != nil || target == "" {
		return err
	}
	if history == nil || m.service.events == nil || m.service.audit == nil {
		return ErrUnavailable
	}
	prior, err := history(ctx, tx, tenant, project, []string{"project.stage_ready"}, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	for _, occurrence := range prior {
		if occurrence.Target == target {
			return nil
		}
	}
	if err := m.service.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, EventType: "project.stage_ready", TargetType: "project", TargetID: project, Detail: map[string]any{"actor_kind": "SYSTEM", "authority_source": "scheduled:delivery.project_health_refresh", "target": target, "criteria_passed": passed}}); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "project.stage_ready", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "scheduled:delivery.project_health_refresh"}, Subject: events.Subject{Kind: "project", ID: project}, Data: map[string]any{"target": target, "criteria_passed": passed}})
	return err
}

func (m *Module) cancelledDependentsOn(ctx context.Context, tx *gorm.DB, tenant, predecessor guid.ID) ([]DependencyNotificationFact, error) {
	if tx == nil || tenant <= 0 || predecessor <= 0 {
		return nil, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	pred, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(predecessor), w.ArchivedAt.IsNull(), w.State.Eq("CANCELLED")).First() //nolint:misspell // Canonical domain status.
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d := query.BindDependencies(tx, m.service.names)
	rows, err := d.WithContext(ctx).Where(d.TenantID.Eq(tenant), d.ProjectID.Eq(pred.ProjectID), d.DependsOnID.Eq(predecessor)).Order(d.WorkItemID).Find()
	if err != nil {
		return nil, err
	}
	out := []DependencyNotificationFact{}
	a := query.BindAssignments(tx, m.service.names)
	for _, dep := range rows {
		work, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(pred.ProjectID), w.ID.Eq(dep.WorkItemID), w.ArchivedAt.IsNull(), w.State.NotIn("ACCEPTED", "CANCELLED")).First() //nolint:misspell // Canonical domain status.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		fact := DependencyNotificationFact{ProjectID: work.ProjectID, WorkItemID: work.ID}
		assignment, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(work.ProjectID), a.WorkItemID.Eq(work.ID), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE"), a.AssigneeKind.Eq("HUMAN")).Order(a.ID.Desc()).First()
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if assignment != nil {
			fact.ExecutorID = assignment.AssigneePrincipalID
		}
		out = append(out, fact)
	}
	return out, nil
}

func (m *Module) provideNotificationFacts(s *module.Services) error {
	if err := module.Provide[ReportSuperior](s, m.reportSuperiorOn); err != nil {
		return err
	}
	if err := module.Provide[HomeFacts](s, m.homeFactsOn); err != nil {
		return err
	}
	if err := module.Provide[HeavyPlanningReferences](s, m.heavyPlanningReferenceOn); err != nil {
		return err
	}
	return module.Provide[DecisionNotifications](s, DecisionNotifications{AttentionWorksOn: m.attentionWorksOn, AcceptedUndeliveredOn: m.acceptedUndeliveredOn, AttentionOn: m.executionAttentionWorkOn, WorkReadableOn: m.notificationWorkReadableOn, AndonOn: m.andonNotificationOn, AndonEligibleOn: m.andonNotificationEligibleOn, PublishStageReadyOn: func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
		history, err := module.Resolve[events.ProjectOccurrences](s)
		if err != nil {
			return err
		}
		return m.publishStageReadyOn(ctx, tx, tenant, project, history)
	}, StageProjectsOn: m.stageNotificationProjectsOn, StageSuggestionOn: m.stageNotificationSuggestionOn, CancelledDependentsOn: m.cancelledDependentsOn, DeadlineOn: m.deadlineNotificationOn, ConstitutionEligibleOn: m.constitutionNotificationEligibleOn, ReviewSheetOn: m.reviewSheetNotificationOn, ReviewSheetEligibleOn: m.reviewSheetNotificationEligibleOn, FactsOn: m.decisionNotificationFactsOn, PlanEligibleOn: m.planNotificationEligibleOn, HumanVerificationOn: m.humanVerificationNotificationOn, LeaseExpiredOn: m.leaseExpiredNotificationOn})
}

type ReviewSheetNotificationFact struct {
	ProjectID, InitiatorID guid.ID
	State                  string
	DueAt                  *time.Time
	Reviewers, Submitted   []guid.ID
}

func (m *Module) reviewSheetNotificationOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (ReviewSheetNotificationFact, bool, error) {
	var out ReviewSheetNotificationFact
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	q := query.BindReviewSheets(tx, m.service.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	v, err := sheetView(r)
	if err != nil {
		return out, false, err
	}
	out = ReviewSheetNotificationFact{ProjectID: r.ProjectID, InitiatorID: r.InitiatorID, State: r.State, DueAt: v.Document.DueAt, Reviewers: v.Document.Reviewers}
	for _, op := range v.Document.Opinions {
		if op.Decision != "" {
			out.Submitted = append(out.Submitted, op.ReviewerID)
		}
	}
	return out, true, nil
}

func (m *Module) reviewSheetNotificationEligibleOn(ctx context.Context, tx *gorm.DB, tenant, id, actor guid.ID, typ string) (bool, error) {
	f, found, err := m.reviewSheetNotificationOn(ctx, tx, tenant, id)
	if err != nil || !found {
		return false, err
	}
	if typ == "review_sheet.rejected" {
		if f.State != "REJECTED" || actor != f.InitiatorID {
			return false, nil
		}
	} else if f.State != "OPEN" || !slices.Contains(f.Reviewers, actor) || slices.Contains(f.Submitted, actor) {
		return false, nil
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
	permission := "review_participate"
	if typ == "review_sheet.rejected" && actor == f.InitiatorID {
		permission = "review_initiate"
	}
	if err = m.sheetActionAuthorityOn(readCtx, tx, f.ProjectID, id, permission); errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrWorkProjectReadOnly) || errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	q := query.BindReviewSheets(tx, m.service.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return false, err
	}
	_, _, err = m.reviewTargetOn(readCtx, tx, r.ProjectID, r.RequirementID, r.WorkItemID)
	if errors.Is(err, ErrWorkForbidden) || errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

type LeaseExpiredNotificationFact struct {
	ProjectID, WorkItemID, AssigneeID guid.ID
	EndedAt                           time.Time
}

func (m *Module) leaseExpiredNotificationOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (LeaseExpiredNotificationFact, bool, error) {
	var out LeaseExpiredNotificationFact
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	q := query.BindAssignments(tx, m.service.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Status.Eq("EXPIRED"), q.EndReason.Eq("LEASE_EXPIRED"), q.AssigneeKind.Eq("HUMAN"), q.Role.Eq("EXECUTOR"), q.EndedAt.IsNotNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return LeaseExpiredNotificationFact{ProjectID: r.ProjectID, WorkItemID: r.WorkItemID, AssigneeID: r.AssigneePrincipalID, EndedAt: *r.EndedAt}, true, nil
}

// humanVerificationNotificationOn mirrors the canonical Human CHECK's source
// row permission, independent reviewer and current claim fences.
func (m *Module) humanVerificationNotificationOn(ctx context.Context, tx *gorm.DB, tenant, id, recipient guid.ID) (guid.ID, guid.ID, bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 || recipient <= 0 {
		return 0, 0, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, 0, false, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	if work.State != "NEEDS_HUMAN_REVIEW" || work.CurrentClaimID == nil || work.PausedAt != nil || work.BaselineStale || work.IsContainer {
		return 0, work.ProjectID, false, nil
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: recipient})
	if _, err := m.readWork(readCtx, tx, tenant, work.ProjectID, id, recipient); errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) {
		return 0, work.ProjectID, false, nil
	} else if err != nil {
		return 0, 0, false, err
	}
	if err := m.authorizeOn(readCtx, tx, tenant, work.ProjectID, "work_item_write"); errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) {
		return 0, work.ProjectID, false, nil
	} else if err != nil {
		return 0, 0, false, err
	}
	constraint, err := m.workPolicy(readCtx, tx, tenant, work.ProjectID, "work_item_write", recipient)
	if err != nil {
		return 0, 0, false, err
	}
	scope, err := workScope(constraint, work.ProjectID, w.CreatedBy)
	if errors.Is(err, ErrWorkForbidden) {
		return 0, work.ProjectID, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	visible := w.WithContext(readCtx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.ProjectID.Eq(work.ProjectID))
	if scope != nil {
		visible = visible.Where(scope)
	}
	if _, err := visible.First(); errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, work.ProjectID, false, nil
	} else if err != nil {
		return 0, 0, false, err
	}
	claim, err := m.resultOn(ctx, tx, tenant, work.ProjectID, id, *work.CurrentClaimID)
	if err != nil {
		return 0, 0, false, err
	}
	if claim.ActorID == recipient || claim.BaselineVersionNo != work.BaselineVersionNo {
		return claim.ID, work.ProjectID, false, nil
	}
	var plan []workVerificationStep
	if err := json.Unmarshal([]byte(claim.VerificationPlan), &plan); err != nil {
		return 0, 0, false, err
	}
	r := query.BindVerificationRuns(tx, m.service.names)
	for _, step := range plan {
		if step.Method != "HUMAN" {
			continue
		}
		n, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(work.ProjectID), r.WorkItemID.Eq(id), r.ResultID.Eq(claim.ID), r.CriterionID.Eq(step.CriterionID), r.Stage.Eq("HUMAN"), r.Method.Eq("HUMAN"), r.State.Eq("DONE"), r.CheckID.IsNotNull()).Count()
		if err != nil {
			return 0, 0, false, err
		}
		if n == 0 {
			return claim.ID, work.ProjectID, true, nil
		}
	}
	return claim.ID, work.ProjectID, false, nil
}

// HomeFacts reads source histories under current project and work row authority.
// It never treats a mutable accepted_at as the first historical acceptance.
type HomeFacts func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (HomeProjectFacts, error)

type HomeAcceptance struct {
	WorkItemID         guid.ID
	At                 time.Time
	PriorQualityRework bool
}

type HomeProjectFacts struct {
	VisibleWorkIDs    []guid.ID            `json:"-"`
	ReworkTimeline    []HomeWorkTransition `json:"-"`
	Activity          []HomeActivity       `json:"-"`
	ActiveLoads       map[guid.ID]int      `json:"-"`
	ProjectID         guid.ID              `json:"project_id"`
	Code              string               `json:"code"`
	Stage             string               `json:"stage"`
	Mode              string               `json:"mode"`
	Health            string               `json:"health"`
	HealthReasons     string               `json:"health_reasons"`
	BudgetCapMicros   *string              `json:"budget_cap_micros"`
	BudgetSpentMicros string               `json:"budget_spent_micros"`
	Accepted          int                  `json:"accepted"`
	Total             int                  `json:"total"`
	FirstAcceptances  []HomeAcceptance     `json:"-"`
	ClaimedDays       int                  `json:"-"`
	EvidencedDays     int                  `json:"-"`
}

type HomeWorkTransition struct {
	WorkItemID guid.ID
	At         time.Time
	Accepted   bool
}

type HomeActivity struct {
	Kind                  string
	ObjectID, PrincipalID guid.ID
	Title                 string
	At                    time.Time
}

func (m *Module) homeFactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, start, end time.Time) (HomeProjectFacts, error) {
	out := HomeProjectFacts{ProjectID: project}
	out.ActiveLoads = map[guid.ID]int{}
	if err := m.authorizeOn(ctx, tx, tenant, project, "project_read"); err != nil {
		return out, err
	}
	facts, err := m.planningFactsOn(ctx, tx, tenant, project, 0, start, end)
	if err != nil {
		return out, err
	}
	p := query.BindProjects(tx, m.service.names)
	row, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).First()
	if err != nil {
		return out, err
	}
	out.Code, out.Stage, out.Mode, out.Health, out.HealthReasons = row.Code, row.Stage, row.TeamMode, row.Health, row.HealthReasons
	out.BudgetSpentMicros = strconv.FormatInt(row.BudgetSpentMicros, 10)
	if row.BudgetCapMicros != nil {
		v := strconv.FormatInt(*row.BudgetCapMicros, 10)
		out.BudgetCapMicros = &v
	}
	ids := make([]driver.Valuer, 0, len(facts.Work))
	titles := map[guid.ID]string{}
	for _, work := range facts.Work {
		out.VisibleWorkIDs = append(out.VisibleWorkIDs, work.ID)
		titles[work.ID] = work.Title
		ids = append(ids, work.ID)
		out.Total++
		if work.State == "ACCEPTED" {
			out.Accepted++
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	assignments := query.BindAssignments(tx, m.service.names)
	activeAssignments, err := assignments.WithContext(ctx).Where(assignments.TenantID.Eq(tenant), assignments.ProjectID.Eq(project), assignments.WorkItemID.In(ids...), assignments.Status.Eq("ACTIVE"), assignments.Role.Eq("EXECUTOR"), assignments.ActiveSlot.IsNotNull(), assignments.EndedAt.IsNull()).Find()
	if err != nil {
		return out, err
	}
	for _, assignment := range activeAssignments {
		if assignment.LeaseExpiresAt == nil || assignment.LeaseExpiresAt.After(time.Now().UTC()) {
			out.ActiveLoads[assignment.AssigneePrincipalID]++
		}
	}
	assigned, err := assignments.WithContext(ctx).Where(assignments.TenantID.Eq(tenant), assignments.ProjectID.Eq(project), assignments.WorkItemID.In(ids...), assignments.Role.Eq("EXECUTOR"), assignments.CreatedAt.Gte(start), assignments.CreatedAt.Lt(end)).Find()
	if err != nil {
		return out, err
	}
	for _, assignment := range assigned {
		out.Activity = append(out.Activity, HomeActivity{Kind: "WORK_ASSIGNED", ObjectID: assignment.WorkItemID, PrincipalID: assignment.AssigneePrincipalID, Title: titles[assignment.WorkItemID], At: assignment.CreatedAt})
	}
	a := query.BindWorkTransitions(tx, m.service.names)
	history, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.In(ids...), a.CreatedAt.Lt(end)).Order(a.CreatedAt, a.ID).Find()
	if err != nil {
		return out, err
	}
	seen, reworked := map[guid.ID]bool{}, map[guid.ID]bool{}
	for _, event := range history {
		if event.ToState == "REWORK" || event.ToState == "ACCEPTED" {
			out.ReworkTimeline = append(out.ReworkTimeline, HomeWorkTransition{WorkItemID: event.WorkItemID, At: event.CreatedAt, Accepted: event.ToState == "ACCEPTED"})
		}
		if !event.CreatedAt.Before(start) && (event.ToState == "ACCEPTED" || event.ToState == "REWORK") {
			kind := "WORK_ACCEPTED"
			if event.ToState == "REWORK" {
				kind = "WORK_REWORK_REQUESTED"
			}
			var actor guid.ID
			if event.ActorID != nil {
				actor = *event.ActorID
			}
			out.Activity = append(out.Activity, HomeActivity{Kind: kind, ObjectID: event.WorkItemID, PrincipalID: actor, Title: titles[event.WorkItemID], At: event.CreatedAt})
		}
		if event.ToState == "REWORK" {
			reworked[event.WorkItemID] = true
		}
		if event.ToState != "ACCEPTED" || seen[event.WorkItemID] {
			continue
		}
		seen[event.WorkItemID] = true
		if !event.CreatedAt.Before(start) {
			out.FirstAcceptances = append(out.FirstAcceptances, HomeAcceptance{WorkItemID: event.WorkItemID, At: event.CreatedAt, PriorQualityRework: reworked[event.WorkItemID]})
		}
	}
	r := query.BindResultVersions(tx, m.service.names)
	claims, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.WorkItemID.In(ids...), r.CreatedAt.Gte(start), r.CreatedAt.Lt(end)).Find()
	if err != nil {
		return out, err
	}
	// The metric counts distinct work/day, not result versions and not work/window.
	claimed, evidenced := map[string]bool{}, map[string]bool{}
	for _, claim := range claims {
		out.Activity = append(out.Activity, HomeActivity{Kind: "WORK_CLAIMED", ObjectID: claim.WorkItemID, PrincipalID: claim.ActorID, Title: titles[claim.WorkItemID], At: claim.CreatedAt})
		key := claim.WorkItemID.String() + ":" + claim.CreatedAt.UTC().Format(time.DateOnly)
		claimed[key] = true
		e := query.BindResultEvidence(tx, m.service.names)
		pins, err := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ProjectID.Eq(project), e.WorkItemID.Eq(claim.WorkItemID), e.ResultID.Eq(claim.ID)).Find()
		if err != nil {
			return out, err
		}
		for _, pin := range pins {
			if len(pin.SHA256) == 64 && pin.BindingID > 0 && pin.VersionID > 0 {
				evidenced[key] = true
			}
		}
	}
	out.ClaimedDays, out.EvidencedDays = len(claimed), len(evidenced)
	return out, nil
}

// pendingHumanPlansOn exposes only proposals that the current Human can both
// read and independently decide. The current source eligibility, not an event
// actor or a historical notification audience, decides inclusion.
func (m *Module) pendingHumanPlansOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]PendingHumanPlan, error) {
	if m == nil || m.service == nil || m.workPolicy == nil {
		return nil, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project < 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrWorkForbidden
	}
	p := query.BindReviewPlans(tx, m.service.names)
	stmt := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.State.Eq("PROPOSED"))
	if project > 0 {
		stmt = stmt.Where(p.ProjectID.Eq(project))
	}
	rows, err := stmt.Find()
	if err != nil {
		return nil, err
	}
	out := []PendingHumanPlan{}
	for _, row := range rows {
		eligible, err := m.planNotificationEligibleOn(ctx, tx, tenant, row.ID, actor)
		if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !eligible {
			continue
		}
		c, err := m.workPolicy(ctx, tx, tenant, row.ProjectID, "work_item_read", actor)
		if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		w := query.BindWorkItems(tx, m.service.names)
		scope, err := workScope(c, row.ProjectID, w.CreatedBy)
		if errors.Is(err, ErrWorkForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		work := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(row.ProjectID), w.ID.Eq(row.WorkItemID), w.ArchivedAt.IsNull())
		if scope != nil {
			work = work.Where(scope)
		}
		if _, err = work.First(); errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return nil, err
		}
		out = append(out, PendingHumanPlan{ID: row.ID, ProjectID: row.ProjectID, CreatedAt: row.CreatedAt})
	}
	return out, nil
}

// HeavyPlanningReferences checks real scoped source identities for a draft;
// source launch retains all current lease, assignment and execution guards.
type HeavyPlanningReferences func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error

func (m *Module) heavyPlanningReferenceOn(ctx context.Context, tx *gorm.DB, tenant, project, work, assignment guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || work <= 0 || assignment <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.workPolicy == nil {
		return ErrUnavailable
	}
	c, err := m.workPolicy(ctx, tx, tenant, project, "work_item_assign", authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, project, w.CreatedBy)
	if err != nil {
		return err
	}
	stmt := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(work), w.ArchivedAt.IsNull())
	if scope != nil {
		stmt = stmt.Where(scope)
	}
	if _, err = stmt.First(); err != nil {
		return err
	}
	a := query.BindAssignments(tx, m.service.names)
	_, err = a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.Eq(work), a.ID.Eq(assignment), a.Role.Eq("EXECUTOR")).First()
	return err
}

func (m *Module) decisionNotificationFactsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string) (DecisionNotificationFact, bool, error) {
	var out DecisionNotificationFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 || !slices.Contains([]string{"plan", "work_item"}, kind) {
		return out, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return out, false, ErrUnavailable
	}
	work := id
	if kind == "plan" {
		p := query.BindReviewPlans(tx, m.service.names)
		r, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		out.ProjectID, out.AuthorID, out.State, work = r.ProjectID, r.ProposedBy, r.State, r.WorkItemID
	}
	w := query.BindWorkItems(tx, m.service.names)
	r, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(work), w.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if kind == "plan" && out.ProjectID != r.ProjectID {
		return out, false, ErrUnavailable
	}
	out.ProjectID, out.WorkItemID, out.AccountableID = r.ProjectID, r.ID, r.AccountablePrincipalID
	if r.EscalationReason != nil {
		out.EscalationReason = *r.EscalationReason
	}
	if out.EscalationReason == "REQUIREMENT_CONFLICT" {
		transitions := query.BindWorkTransitions(tx, m.service.names)
		prior, err := transitions.WithContext(ctx).Where(transitions.TenantID.Eq(tenant), transitions.WorkItemID.Eq(r.ID), transitions.ProjectID.Eq(r.ProjectID), transitions.ReasonCode.Eq("REQUIREMENT_CONFLICT_RAISE")).Order(transitions.VersionAfter.Desc()).First()
		if err != nil {
			return out, false, err
		}
		out.ConflictVersion = prior.VersionAfter
	}

	if kind == "work_item" {
		out.State = r.State
	}
	return out, true, nil
}

// Eligibility mirrors the canonical Human plan command's row constraints,
// separation and current-state guards, without executing a decision.
func (m *Module) planNotificationEligibleOn(ctx context.Context, tx *gorm.DB, tenant, id, principal guid.ID) (bool, error) {
	f, exists, err := m.decisionNotificationFactsOn(ctx, tx, tenant, id, "plan")
	if err != nil || !exists {
		return false, err
	}
	if principal <= 0 || f.AuthorID == principal || f.State != "PROPOSED" {
		return false, nil
	}
	if m.workPolicy == nil || m.principals.ActiveHumanByID == nil || m.members == nil {
		return false, ErrUnavailable
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, principal)
	if err != nil || !active || human.ID != principal {
		return false, err
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, principal)
	if err != nil || !active {
		return false, err
	}
	c, err := m.workPolicy(ctx, tx, tenant, f.ProjectID, "plan_approve", principal)
	if err != nil {
		return false, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, f.ProjectID, w.CreatedBy)
	if err != nil {
		return false, err
	}
	stmt := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(f.ProjectID), w.ID.Eq(f.WorkItemID), w.ArchivedAt.IsNull(), w.CurrentExecutionID.IsNull())
	if scope != nil {
		stmt = stmt.Where(scope)
	}
	work, err := stmt.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if slices.Contains([]string{"ACCEPTED", "CANCELLED"}, work.State) { //nolint:misspell // Frozen domain states.
		return false, nil
	}
	p := query.BindProjects(tx, m.service.names)
	project, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(f.ProjectID), p.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if slices.Contains([]string{"CLOSED", "CANCELLED"}, project.Stage) { //nolint:misspell // Frozen domain states.
		return false, nil
	}
	plans := query.BindReviewPlans(tx, m.service.names)
	plan, err := plans.WithContext(ctx).Where(plans.TenantID.Eq(tenant), plans.ID.Eq(id)).First()
	if err != nil {
		return false, err
	}
	view, err := reviewPlanView(plan)
	if err != nil {
		return false, err
	}
	err = m.validatePlanCoverageOn(ctx, tx, work, view.Content)
	if errors.Is(err, ErrResultCoverage) || errors.Is(err, ErrInvalidArgument) {
		return false, nil
	}
	return err == nil, err
}

// Rule 11: expose current source existence without inventing an Agent decision
// command. acceptResultOn/lockedWorkOn currently require an active Human and an
// independent Human CHECK. Calling them as SYSTEM or a borrowed Human is invalid.
func (m *Module) bindAutomatedDecisions(s *module.Services) error {
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	if connector, err := module.Resolve[governance.QualityReviewConnector](s); err == nil {
		if connector.Bind == nil {
			return ErrUnavailable
		}
		if err = connector.Bind(m.qualityReviewWindowOn); err != nil {
			return err
		}
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	if sources, err := module.Resolve[governance.InboxSources](s); err == nil {
		leads, leadErr := module.Resolve[governance.ProjectLead](s)
		if leadErr != nil && !errors.Is(leadErr, module.ErrServiceMissing) {
			return leadErr
		}
		if err = sources.Register(governance.InboxSource{Kind: "PLAN_APPROVAL", Read: m.planApprovalInbox}); err != nil {
			return err
		}
		if err = sources.Register(governance.InboxSource{Kind: "ANDON", Read: func(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
			return m.andonInbox(ctx, req, leads)
		}}); err != nil {
			return err
		}
		for _, kind := range []string{"HUMAN_VERIFICATION", "ESCALATION_INTERNAL_EXHAUSTED"} {
			if err = sources.Register(governance.InboxSource{Kind: kind, Read: func(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
				return m.verificationInboxWithLeads(ctx, req, leads)
			}}); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	c, err := module.Resolve[governance.AutomatedDecisionConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(governance.AutomatedDecisionSource{ResolveOn: m.automatedDecisionOn})
}

func (m *Module) qualityReviewWindowOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, start, end time.Time) (governance.QualityReviewWindow, error) {
	out := governance.QualityReviewWindow{WorkIDs: []guid.ID{}}
	if !start.Before(end) {
		return out, ErrInvalidArgument
	}
	if err := m.authorizeOn(ctx, tx, tenant, project, "project_read"); err != nil {
		return out, err
	}
	facts, err := m.planningFactsOn(ctx, tx, tenant, project, 0, start, end)
	if err != nil {
		return out, err
	}
	ids := make([]driver.Valuer, 0, len(facts.Work))
	for _, work := range facts.Work {
		out.WorkIDs = append(out.WorkIDs, work.ID)
		ids = append(ids, work.ID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	q := query.BindWorkTransitions(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.In(ids...), q.ToState.Eq("NEEDS_HUMAN_REVIEW"), q.CreatedAt.Gte(start.UTC()), q.CreatedAt.Lt(end.UTC())).Find()
	if err != nil {
		return out, err
	}
	seen := map[guid.ID]bool{}
	for _, row := range rows {
		seen[row.WorkItemID] = true
	}
	out.Entered = len(seen)
	return out, nil
}

// verificationInbox reads actual frozen HUMAN verification instructions
// and the verification owner's explicit exhaustion reason. Generic failure or
// a work item's creator never substitutes for either lifecycle fact.
func (m *Module) verificationInboxWithLeads(ctx context.Context, req governance.InboxRequest, leads governance.ProjectLead) ([]governance.InboxItem, error) {
	if req.PrincipalID <= 0 || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.TenantID != authn.TenantIDFromContext(ctx) {
		return nil, governance.ErrForbidden
	}
	if req.ProjectID < 0 || req.Limit < 1 || req.Limit > 101 || (req.Kind != "HUMAN_VERIFICATION" && req.Kind != "ESCALATION_INTERNAL_EXHAUSTED") {
		return nil, governance.ErrInvalidArgument
	}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, governance.ErrUnavailable
	}
	out := []governance.InboxItem{}
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindWorkItems(tx, m.service.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.Eq("NEEDS_HUMAN_REVIEW"), q.ArchivedAt.IsNull(), q.PausedAt.IsNull(), q.CurrentClaimID.IsNotNull(), q.BaselineStale.Is(false))
		if req.ProjectID > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(req.ProjectID))
		}
		rows, err := stmt.Find()
		if err != nil {
			return err
		}
		for _, work := range rows {
			if _, err := m.readWork(ctx, tx, req.TenantID, work.ProjectID, work.ID, req.PrincipalID); errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) {
				continue
			} else if err != nil {
				return err
			}
			permission := "work_item_write"
			if req.Kind == "ESCALATION_INTERNAL_EXHAUSTED" {
				permission = "work_item_read"
			}
			if err := m.authorizeOn(ctx, tx, req.TenantID, work.ProjectID, permission); errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) {
				continue
			} else if err != nil {
				return err
			}
			// Match the canonical mutation's work row data scope as well as its action.
			policy, err := m.workPolicy(ctx, tx, req.TenantID, work.ProjectID, permission, req.PrincipalID)
			if err != nil {
				return err
			}
			scope, err := workScope(policy, work.ProjectID, q.CreatedBy)
			if errors.Is(err, ErrWorkForbidden) {
				continue
			} else if err != nil {
				return err
			}
			visible := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.ID.Eq(work.ID), q.ProjectID.Eq(work.ProjectID))
			if scope != nil {
				visible = visible.Where(scope)
			}
			if _, err := visible.First(); errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				return err
			}
			claim, err := m.resultOn(ctx, tx, req.TenantID, work.ProjectID, work.ID, *work.CurrentClaimID)
			if err != nil {
				return err
			}
			if req.Kind == "HUMAN_VERIFICATION" && claim.ActorID == req.PrincipalID {
				continue
			}
			priority := "NORMAL"
			if req.Kind == "ESCALATION_INTERNAL_EXHAUSTED" {
				if work.EscalationReason == nil || *work.EscalationReason != "INTERNAL_ROUNDS_EXHAUSTED" || !work.RequireHumanDecision {
					continue
				}
				if work.AccountablePrincipalID != req.PrincipalID {
					if leads == nil {
						continue
					}
					lead, err := leads(ctx, tx, req.TenantID, work.ProjectID, req.PrincipalID)
					if err != nil {
						return err
					}
					if !lead {
						continue
					}
				}
				priority = "HIGH"
			} else {
				var plan []workVerificationStep
				if err := json.Unmarshal([]byte(claim.VerificationPlan), &plan); err != nil {
					return err
				}
				r := query.BindVerificationRuns(tx, m.service.names)
				pending := false
				for _, step := range plan {
					if step.Method != "HUMAN" {
						continue
					}
					n, err := r.WithContext(ctx).Where(r.TenantID.Eq(req.TenantID), r.ProjectID.Eq(work.ProjectID), r.WorkItemID.Eq(work.ID), r.ResultID.Eq(claim.ID), r.CriterionID.Eq(step.CriterionID), r.Stage.Eq("HUMAN"), r.Method.Eq("HUMAN"), r.State.Eq("DONE"), r.CheckID.IsNotNull()).Count()
					if err != nil {
						return err
					}
					pending = pending || n == 0
				}
				if !pending {
					continue
				}
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Kind: req.Kind, ID: claim.ID, Priority: priority, CreatedAt: claim.CreatedAt}, ProjectID: work.ProjectID, Title: work.Title, ObjectType: "work_item", ObjectID: work.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
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

func (m *Module) planApprovalInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	if req.PrincipalID <= 0 || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.TenantID != authn.TenantIDFromContext(ctx) {
		return nil, ErrWorkForbidden
	}
	if req.Kind != "PLAN_APPROVAL" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, ErrInvalidArgument
	}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, governance.ErrUnavailable
	}
	out := []governance.InboxItem{}
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := m.pendingHumanPlansOn(ctx, tx, req.TenantID, req.ProjectID)
		if errors.Is(err, ErrUnavailable) {
			return governance.ErrUnavailable
		}
		if err != nil {
			return err
		}
		for _, row := range rows {
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "NORMAL", CreatedAt: row.CreatedAt, Kind: req.Kind, ID: row.ID}, ProjectID: row.ProjectID, Title: "PLAN_APPROVAL · " + row.ID.String(), ObjectType: "review_plan", ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
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

func (m *Module) automatedDecisionOn(ctx context.Context, tx *gorm.DB, tenant, project, work, claim guid.ID) (governance.AuthorityRequest, bool, error) {
	_, current, err := m.claimEvaluationOn(ctx, tx, tenant, project, work, claim)
	if err != nil {
		return governance.AuthorityRequest{}, false, err
	}
	if !current {
		return governance.AuthorityRequest{}, false, nil
	}
	return governance.AuthorityRequest{}, true, governance.ErrUnavailable
}

// constitutionNotificationEligibleOn follows the same current Human authority
// as the actual review command, without creating a decision or copying its body.
func (m *Module) constitutionNotificationEligibleOn(ctx context.Context, tx *gorm.DB, tenant, id, actor guid.ID) (guid.ID, bool, error) {
	if tx == nil || tenant <= 0 || id <= 0 || actor <= 0 {
		return 0, false, ErrInvalidArgument
	}
	q := query.BindConstitutions(tx, m.service.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if r.State != "IN_REVIEW" {
		return r.ProjectID, false, nil
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
	_, kind, err := m.constitutionProjectOn(readCtx, tx, tenant, r.ProjectID, actor, "project_manage")
	if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) || errors.Is(err, ErrWorkProjectReadOnly) || errors.Is(err, ErrConstitutionHumanOnly) || errors.Is(err, ErrInvalidMember) {
		return r.ProjectID, false, nil
	}
	return r.ProjectID, err == nil && kind == "HUMAN", err
}

type DeadlineNotificationFact struct {
	ProjectID guid.ID
	Active    bool
	ChangedAt time.Time
}

// DeadlineNotificationOn exposes the current source episode, never UpdatedAt.
func (m *Module) deadlineNotificationOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string) (DeadlineNotificationFact, bool, error) {
	var out DeadlineNotificationFact
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	switch kind {
	case "review_age":
		q := query.BindWorkItems(tx, m.service.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.ArchivedAt.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		return DeadlineNotificationFact{ProjectID: r.ProjectID, Active: r.State == "NEEDS_HUMAN_REVIEW", ChangedAt: r.StateChangedAt}, true, nil
	case "project_blocked":
		q := query.BindProjects(tx, m.service.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(id), q.ArchivedAt.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		return DeadlineNotificationFact{ProjectID: r.ProjectID, Active: r.Stage == "BLOCKED", ChangedAt: r.StageChangedAt}, true, nil
	default:
		return out, false, ErrInvalidArgument
	}
}

// Accepted candidates are canonical ACCEPTED facts. External READY/SENT membership
// is not available in this source; consumers must expose that coverage limit.
func (m *Module) acceptedUndeliveredOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, at time.Time) ([]guid.ID, error) {
	if !validTransaction(tx) || tenant <= 0 || project <= 0 || at.IsZero() {
		return nil, ErrInvalidArgument
	}
	p := query.BindProjects(tx, m.service.names)
	if _, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).First(); err != nil {
		return nil, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	rows, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.State.Eq("ACCEPTED"), w.AcceptedAt.Lt(at.Add(-24*time.Hour)), w.IsContainer.Is(false), w.ArchivedAt.IsNull()).Order(w.ID).Find()
	if err != nil {
		return nil, err
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}
func validTransaction(tx *gorm.DB) bool {
	if tx == nil || tx.Statement == nil {
		return false
	}
	_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
	return ok
}

// ExecutionAttentionFact records current work state and the actual executor
// assignment, including closed assignments retained after an execution failure.
type ExecutionAttentionFact struct {
	ProjectID, WorkItemID, ExecutionID, ExecutorID guid.ID
	ExecutorKind, State                            string
	Version                                        int
	ChangedAt                                      time.Time
}

func (m *Module) executionAttentionWorkOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (ExecutionAttentionFact, bool, error) {
	var f ExecutionAttentionFact
	if !validTransaction(tx) || tenant <= 0 || id <= 0 {
		return f, false, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	r, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return f, false, nil
	}
	if err != nil {
		return f, false, err
	}
	f = ExecutionAttentionFact{ProjectID: r.ProjectID, WorkItemID: r.ID, State: r.State, Version: r.Version, ChangedAt: r.StateChangedAt}
	if r.CurrentExecutionID != nil {
		f.ExecutionID = *r.CurrentExecutionID
	}
	a := query.BindAssignments(tx, m.service.names)
	row, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.WorkItemID.Eq(id), a.ProjectID.Eq(r.ProjectID), a.Role.Eq("EXECUTOR")).Order(a.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return f, true, nil
	}
	if err != nil {
		return f, false, err
	}
	f.ExecutorID = row.AssigneePrincipalID
	f.ExecutorKind = row.AssigneeKind
	return f, true, nil
}

// ReportSuperior resolves the delivery lead only when the subject belongs to
// exactly one current ACTIVE project. It uses live IAM entity bindings.
type ReportSuperior func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error)

func (m *Module) reportSuperiorOn(ctx context.Context, tx *gorm.DB, tenant, subject guid.ID) (guid.ID, error) {
	if tx == nil || tenant <= 0 || subject <= 0 {
		return 0, ErrInvalidArgument
	}
	if m.roles.RoleIDOn == nil {
		return 0, ErrUnavailable
	}
	var ids []guid.ID
	err := tx.WithContext(ctx).Table(m.service.names.Projects.String()+" AS p").Select("p.project_id").Distinct().Joins("JOIN "+m.projectAccess.RoleBindings.String()+" AS b ON b.tenant_id=p.tenant_id AND b.scope_id=p.project_id").Where("p.tenant_id=? AND p.stage=? AND p.archived_at IS NULL AND b.principal_id=? AND b.scope_type=? AND b.effect=? AND (b.expires_at IS NULL OR b.expires_at>?)", tenant, "ACTIVE", subject, "entity", "allow", time.Now().UTC()).Limit(2).Scan(&ids).Error
	if err != nil || len(ids) != 1 {
		return 0, err
	}
	role, err := m.roles.RoleIDOn(ctx, tx, tenant, "delivery_lead")
	if err != nil {
		return 0, err
	}
	var leads []guid.ID
	err = tx.WithContext(ctx).Table(m.projectAccess.RoleBindings.String()).Select("principal_id").Where("tenant_id=? AND scope_id=? AND scope_type=? AND role_id=? AND effect=? AND (expires_at IS NULL OR expires_at>?)", tenant, ids[0], "entity", role, "allow", time.Now().UTC()).Order("principal_id").Limit(2).Scan(&leads).Error
	if err != nil || len(leads) != 1 {
		return 0, err
	}
	return leads[0], nil
}

func (m *Module) attentionWorksOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]guid.ID, error) {
	if !validTransaction(tx) || tenant <= 0 || project < 0 {
		return nil, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.State.Eq("EXECUTION_FAILED"), w.ArchivedAt.IsNull())
	if project > 0 {
		base = base.Where(w.ProjectID.Eq(project))
	}
	rows, err := base.Find()
	if err != nil {
		return nil, err
	}
	out := make([]guid.ID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out, nil
}
