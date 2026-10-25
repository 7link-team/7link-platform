package delivery

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gen"
	"gorm.io/gorm"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// DispatchDecisionHistory contains immutable claims and actual domain decisions,
// including returns before later acceptance. Runner receipt states are absent.
type DispatchClaimFact struct {
	ID, ActorID guid.ID
	ExecutionID *guid.ID
	CreatedAt   time.Time
}
type DispatchDecisionFact struct {
	ID, ClaimID guid.ID
	Action      string
	CreatedAt   time.Time
}
type DispatchHistoryWork struct {
	ProjectID, WorkItemID guid.ID
	Claims                []DispatchClaimFact
	Decisions             []DispatchDecisionFact
	AcceptedClaimID       *guid.ID
	AcceptedAt            *time.Time
}
type DispatchDecisionHistory struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, string, time.Time) ([]DispatchHistoryWork, error)
}

func (m *Module) provideDispatchHistory(s *module.Services) error {
	if err := module.Provide[IntakeDelegatedConversion](s, IntakeDelegatedConversion{ReadOn: m.readDelegatedNewCardOn, AuthorizeOn: m.authorizeDelegatedNewOn, CreateOn: m.createDelegatedNewOn}); err != nil {
		return err
	}
	if err := module.Provide[DispatchStarvationFacts](s, DispatchStarvationFacts{ListOn: m.dispatchStarvedListOn, CurrentOn: m.dispatchStarvedCurrentOn}); err != nil {
		return err
	}
	if err := module.Provide[RejectedWorkDisposition](s, RejectedWorkDisposition{AuthorizeOn: m.reworkDispositionAuthorityOn, ReopenOn: m.reopenRejectedWorkOn, NewRequirementOn: m.newReworkRequirementOn}); err != nil {
		return err
	}
	if err := module.Provide[ClarificationDraftAccess](s, ClarificationDraftAccess{AuthorizeOn: m.clarificationDraftAuthorityOn, SourceOn: m.clarificationDraftSourceOn}); err != nil {
		return err
	}
	if err := module.Provide[ClientSignerFacts](s, ClientSignerFacts{ContactOn: m.clientSignerContactOn}); err != nil {
		return err
	}
	if err := module.Provide[IntakeProjectCreation](s, IntakeProjectCreation{Prepare: m.prepareIntakeProject, CreateOn: m.createIntakeProjectOn}); err != nil {
		return err
	}
	if err := module.Provide[DispatchScheduleFacts](s, DispatchScheduleFacts{ProjectsOn: m.dispatchScheduleProjectsOn, ReadyOn: m.dispatchScheduleReadyOn}); err != nil {
		return err
	}
	return module.Provide[DispatchDecisionHistory](s, DispatchDecisionHistory{ReadOn: m.dispatchDecisionHistoryOn})
}

type ClarificationDraftAccess struct {
	AuthorizeOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error)
	SourceOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error
}

func (m *Module) clarificationDraftAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (string, error) {
	if authn.TenantIDFromContext(ctx) != tenant {
		return "", ErrInvalidArgument
	}
	_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "scope_change")
	return kind, err
}
func (m *Module) clarificationDraftSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID, expected int) error {
	if _, err := m.clarificationDraftAuthorityOn(ctx, tx, tenant, project); err != nil {
		return err
	}
	if m.sources.GetOn == nil {
		return ErrUnavailable
	}
	fact, found, err := m.sources.GetOn(ctx, tx, tenant, source)
	if err != nil {
		return err
	}
	if !found || fact.ProjectID == nil || *fact.ProjectID != project || fact.Version != expected || (fact.Status != "RECEIVED" && fact.Status != "TRIAGED") || (fact.Kind != "NEW" && fact.Kind != "REWORK") {
		return ErrResultState
	}
	return nil
}

// ClientSignerFacts resolves an actual current project contact for the signed-in
// principal. Project membership alone never grants client acceptance authority.
type ClientSignerFacts struct {
	ContactOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error)
}

func (m *Module) clientSignerContactOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) (guid.ID, error) {
	if m == nil || m.service == nil || m.sources.ContactsOn == nil {
		return 0, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || principal <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != principal {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	q := query.BindContacts(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.CanAccept.Is(true)).Order(q.IntakeContactID).Limit(21).Find()
	if err != nil {
		return 0, err
	}
	if len(rows) > 20 {
		return 0, ErrUnavailable
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.IntakeContactID)
	}
	contacts, err := m.sources.ContactsOn(ctx, tx, tenant, ids)
	if err != nil {
		return 0, err
	}
	for _, contact := range contacts {
		if slices.Contains(ids, contact.ID) && contact.TenantID == tenant && contact.PrincipalID != nil && *contact.PrincipalID == principal && contact.State == "ACTIVE" && contact.ArchivedAt == nil {
			return contact.ID, nil
		}
	}
	return 0, ErrWorkForbidden
}

type IntakeProjectInput struct {
	Name string `json:"name" minLength:"1" maxLength:"200"`
	Code string `json:"code" pattern:"^[A-Z][A-Z0-9-]{1,15}$"`
}
type IntakeProjectResult struct{ ProjectID, RequirementID, ChannelID guid.ID }
type IntakeProjectCreation struct {
	Prepare  func(context.Context, *gorm.DB, guid.ID, IntakeProjectInput) ([]guid.ID, error)
	CreateOn func(context.Context, *gorm.DB, guid.ID, guid.ID, IntakeProjectInput, intake.TriageDraft, []guid.ID, string, string) (IntakeProjectResult, error)
}

func intakeProjectBody(in IntakeProjectInput) (projectCreateBody, error) {
	yes := true
	if !utf8.ValidString(in.Name) || strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 200 || !regexp.MustCompile(`^[A-Z][A-Z0-9-]{1,15}$`).MatchString(in.Code) {
		return projectCreateBody{}, ErrInvalidArgument
	}
	return projectCreateBody{Name: strings.TrimSpace(in.Name), Code: in.Code, BudgetNotifyPct: 80, PortalSettings: projectPortalInput{ShowAILabel: &yes, ShowWorkItemTitles: &yes, ShowEvidenceSummary: &yes, ShowReleaseNotes: &yes, MilestonesVisible: &yes, ContributorDisplay: "TEAM_ONLY"}}, nil
}
func (m *Module) prepareIntakeProject(ctx context.Context, db *gorm.DB, tenant guid.ID, in IntakeProjectInput) ([]guid.ID, error) {
	body, err := intakeProjectBody(in)
	if err != nil {
		return nil, err
	}
	return m.prepareProjectCreation(ctx, db, tenant, authn.PrincipalIDFromContext(ctx), body)
}
func (m *Module) createIntakeProjectOn(ctx context.Context, tx *gorm.DB, tenant, source guid.ID, in IntakeProjectInput, draft intake.TriageDraft, ids []guid.ID, cookie, proof string) (IntakeProjectResult, error) {
	out := IntakeProjectResult{}
	if m == nil || m.scopeReauthentication == nil || m.sources.GetOn == nil {
		return out, ErrUnavailable
	}
	body, err := intakeProjectBody(in)
	if err != nil {
		return out, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if len(ids) != 1 || ids[0] != actor {
		return out, ErrInvalidArgument
	}
	if err = m.scopeReauthentication(ctx, tx, actor, cookie, proof, "intake_convert_privileged", "intake:"+source.String()); err != nil {
		return out, err
	}
	fact, found, err := m.sources.GetOn(ctx, tx, tenant, source)
	if err != nil {
		return out, err
	}
	if !found || fact.Kind != "NEW" || fact.Status != "TRIAGED" || fact.ContactID <= 0 {
		return out, ErrInvalidArgument
	}
	if fact.ProjectID == nil {
		return out, ErrInvalidArgument
	}
	if _, kind, err := m.constitutionProjectOn(ctx, tx, tenant, *fact.ProjectID, actor, "scope_change"); err != nil {
		return out, err
	} else if kind != "HUMAN" {
		return out, ErrProjectNonDelegable
	}
	yes := true
	body.OriginIntakeID = &source
	body.Contacts = []projectContactInput{{IntakeContactID: fact.ContactID, Role: "PRIMARY", CanAccept: &yes, ReceivesDeliveries: &yes}}
	created, err := m.createProjectOn(ctx, tx, tenant, actor, body, collab.ProvisioningAudit{}, ids)
	if err != nil {
		return out, err
	}
	criteria := make([]requirementCriterionInput, len(draft.Criteria))
	for i, text := range draft.Criteria {
		criteria[i] = requirementCriterionInput{Text: text, Required: true}
	}
	requirement, err := m.createRequirementOn(ctx, tx, tenant, created.project.ProjectID, actor, requirementCreateBody{Title: draft.Summary, Description: draft.RequirementDraft, Criteria: criteria})
	if err != nil {
		return out, err
	}
	q := query.BindRequirements(tx, m.service.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(requirement.ID)).UpdateSimple(q.SourceIntakeID.Value(source)); err != nil {
		return out, err
	}
	return IntakeProjectResult{ProjectID: created.project.ProjectID, RequirementID: requirement.ID, ChannelID: created.channelID}, nil
}

// DispatchScheduleFacts is a system-job discovery capability, not assignment
// authority. Consumers must execute the canonical command as the current lead;
// that command locks and rechecks identity, membership, project and policy.
type DispatchScheduledWork struct {
	ID             guid.ID
	Version        int
	ReadyAt        time.Time
	Priority, Risk string
}
type DispatchScheduleBatch struct {
	PrincipalID guid.ID
	Items       []DispatchScheduledWork
}
type DispatchScheduleFacts struct {
	ProjectsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	ReadyOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (DispatchScheduleBatch, error)
}

func (m *Module) dispatchScheduleProjectsOn(ctx context.Context, tx *gorm.DB, tenant, after guid.ID) ([]guid.ID, error) {
	if m == nil || m.service == nil || tx == nil || tenant <= 0 || after < 0 {
		return nil, ErrInvalidArgument
	}
	p := query.BindProjects(tx, m.service.names)
	rows, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Gt(after), p.Stage.Eq("ACTIVE"), p.DispatchPolicy.Eq("AUTO"), p.ArchivedAt.IsNull(), p.LeadAgentID.IsNotNull()).Order(p.ProjectID).Limit(100).Find()
	if err != nil {
		return nil, err
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ProjectID)
	}
	return ids, nil
}
func (m *Module) dispatchScheduleReadyOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, at time.Time) (DispatchScheduleBatch, error) {
	out := DispatchScheduleBatch{Items: []DispatchScheduledWork{}}
	if m == nil || m.service == nil || tx == nil || tenant <= 0 || project <= 0 || at.IsZero() {
		return out, ErrInvalidArgument
	}
	p := query.BindProjects(tx, m.service.names)
	rows, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.Stage.Eq("ACTIVE"), p.DispatchPolicy.Eq("AUTO"), p.ArchivedAt.IsNull(), p.LeadAgentID.IsNotNull()).Find()
	if err != nil || len(rows) == 0 {
		return out, err
	}
	if m.agents.ActiveByRoleOn == nil {
		return out, ErrUnavailable
	}
	leads, err := m.agents.ActiveByRoleOn(ctx, tx, tenant, "LEAD")
	if err != nil {
		return out, err
	}
	for _, lead := range leads {
		if lead.ID == *rows[0].LeadAgentID && lead.State == "ACTIVE" {
			out.PrincipalID = lead.PrincipalID
		}
	}
	if out.PrincipalID <= 0 {
		return out, nil
	}
	w := query.BindWorkItems(tx, m.service.names)
	works, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.State.Eq("READY"), w.IsContainer.Is(false), w.ArchivedAt.IsNull()).Order(w.ID).Limit(5001).Find()
	if err != nil {
		return out, err
	}
	if len(works) > 5000 {
		return out, ErrUnavailable
	}
	for _, work := range works {
		priority := ""
		if work.Priority != nil {
			priority = *work.Priority
		}
		out.Items = append(out.Items, DispatchScheduledWork{ID: work.ID, Version: work.Version, ReadyAt: work.StateChangedAt, Priority: priority, Risk: work.Risk})
	}
	slices.SortFunc(out.Items, func(a, b DispatchScheduledWork) int {
		agedA, agedB := a.ReadyAt.Before(at.Add(-2*time.Hour)), b.ReadyAt.Before(at.Add(-2*time.Hour))
		if agedA != agedB {
			if agedA {
				return -1
			}
			return 1
		}
		priority := map[string]int{"URGENT": 4, "HIGH": 3, "MEDIUM": 2, "LOW": 1}
		risk := map[string]int{"HIGH": 3, "MEDIUM": 2, "LOW": 1}
		if priority[a.Priority] != priority[b.Priority] {
			return priority[b.Priority] - priority[a.Priority]
		}
		if risk[a.Risk] != risk[b.Risk] {
			return risk[b.Risk] - risk[a.Risk]
		}
		if !a.ReadyAt.Equal(b.ReadyAt) {
			if a.ReadyAt.Before(b.ReadyAt) {
				return -1
			}
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out, nil
}
func (m *Module) dispatchDecisionHistoryOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, at time.Time) ([]DispatchHistoryWork, error) {
	out := []DispatchHistoryWork{}
	if m == nil || m.service == nil || tx == nil || tx.Statement == nil || tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 || kind == "" || at.IsZero() {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if m.listConstraint == nil {
		return out, ErrUnavailable
	}
	at = at.UTC()
	c, err := m.listConstraint(ctx, tenant, "work_item_read", authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return out, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := personalWorkScope(c, 0, w.ProjectID, w.CreatedBy)
	if err != nil {
		return out, err
	}
	request := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.Type.Eq(kind), w.CreatedAt.Lte(at))
	if scope != nil {
		request = request.Where(scope)
	}
	works, err := request.Order(w.ID).Limit(5001).Find()
	if err != nil {
		return out, err
	}
	if len(works) > 5000 {
		return out, ErrUnavailable
	}
	r := query.BindResultVersions(tx, m.service.names)
	a := query.BindResultActions(tx, m.service.names)
	for _, work := range works {
		fact := DispatchHistoryWork{ProjectID: work.ProjectID, WorkItemID: work.ID, Claims: []DispatchClaimFact{}, Decisions: []DispatchDecisionFact{}}
		claims, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(work.ProjectID), r.WorkItemID.Eq(work.ID), r.CreatedAt.Lte(at)).Order(r.CreatedAt, r.ID).Limit(1001).Find()
		if err != nil {
			return out, err
		}
		if len(claims) > 1000 {
			return out, ErrUnavailable
		}
		for _, claim := range claims {
			fact.Claims = append(fact.Claims, DispatchClaimFact{ID: claim.ID, ActorID: claim.ActorID, ExecutionID: claim.SourceExecutionID, CreatedAt: claim.CreatedAt})
		}
		actions, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(work.ProjectID), a.WorkItemID.Eq(work.ID), a.Kind.In("ACCEPT", "RETURN"), a.CreatedAt.Lte(at)).Order(a.CreatedAt, a.ID).Limit(1001).Find()
		if err != nil {
			return out, err
		}
		if len(actions) > 1000 {
			return out, ErrUnavailable
		}
		for _, action := range actions {
			value := "REQUEST_CHANGES"
			if action.Kind == "ACCEPT" {
				value = "APPROVE"
			}
			fact.Decisions = append(fact.Decisions, DispatchDecisionFact{ID: action.ID, ClaimID: action.ResultID, Action: value, CreatedAt: action.CreatedAt})
			if work.AcceptedDecisionID != nil && *work.AcceptedDecisionID == action.ID && action.Kind == "ACCEPT" && work.AcceptedAt != nil && !work.AcceptedAt.After(at) {
				id := action.ResultID
				fact.AcceptedClaimID = &id
				fact.AcceptedAt = work.AcceptedAt
			}
		}
		out = append(out, fact)
	}
	return out, nil
}

// RejectedWorkDisposition applies canonical domain commands to receipt-verified
// items. The caller owns the immutable rejected subset and disposition ledger.
type RejectedWorkDisposition struct {
	AuthorizeOn      func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	ReopenOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, intake.ReworkItem, int, string) error
	NewRequirementOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, intake.TriageDraft) (guid.ID, error)
}

func (m *Module) reworkDispositionAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	p, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "decision_request_changes")
	if err != nil {
		return err
	}
	if kind != "HUMAN" {
		return ErrConstitutionHumanOnly
	}
	if !slices.Contains([]string{"ACTIVE", "BLOCKED", "ACCEPTANCE_PENDING"}, p.Stage) {
		return ErrResultState
	}
	return nil
}
func (m *Module) reopenRejectedWorkOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, item intake.ReworkItem, expected int, reason string) error {
	if err := m.reworkDispositionAuthorityOn(ctx, tx, tenant, project); err != nil {
		return err
	}
	reason, err := resultText(reason)
	if err != nil {
		return err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	w := query.BindWorkItems(tx, m.service.names)
	current, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(item.WorkItemID), w.ArchivedAt.IsNull()).Take()
	if err != nil {
		return err
	}
	work, result, err := m.lockResultOn(ctx, tx, tenant, project, item.WorkItemID, item.ResultID, actor, current.AccountablePrincipalID, expected, "work_item_read")
	if err != nil {
		return err
	}
	if work.State != "ACCEPTED" || work.AcceptedAt == nil || work.AcceptedDecisionID == nil || work.RequirementID == nil || *work.RequirementID != item.RequirementID {
		return ErrResultState
	}
	if result.ActorID == actor {
		return ErrResultIndependent
	}
	e := query.BindResultEvidence(tx, m.service.names)
	count, err := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ProjectID.Eq(project), e.WorkItemID.Eq(item.WorkItemID), e.ResultID.Eq(item.ResultID), e.ID.Eq(item.EvidenceID)).Count()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrResultState
	}
	source, err := m.resultAuthorityOn(ctx, tx, work, result, actor, "decision_request_changes", 0)
	if err != nil {
		return err
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	action, err := m.appendResultActionOn(ctx, tx, work, result, actor, "RETURN", "FAILED", reason, source, nil, at)
	if err != nil {
		return err
	}
	if err = m.publishQualityEscapeOn(ctx, tx, work, result, action.ID, actor, reason); err != nil {
		return err
	}
	work.State = "REWORK"
	work.AcceptedAt = nil
	work.AcceptedDecisionID = nil
	work.QualityReworkCount++
	work.DefectStage = nil
	if work.QualityReworkCount >= 3 {
		r := "AUTONOMY_REWORK_LIMIT"
		work.PausedAt = &at
		work.PauseReason = &r
		work.RequireHumanDecision = true
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, "ACCEPTED", "RESULT_RETURNED", source, result.ID, at); err != nil {
		return err
	}
	if work.QualityReworkCount >= 3 {
		event := audit.NewDomainEvent(ctx, tenant, "delivery.work_item_escalated", "work_item", work.ID)
		event.Detail["trigger"] = "REWORK_LIMIT"
		event.Detail["count"] = work.QualityReworkCount
		if err = m.service.audit(ctx, tx, event); err != nil {
			return err
		}
		if _, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.escalated", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, SubjectVersion: int64(work.Version), Data: map[string]any{"work_item_id": work.ID.String(), "trigger": "REWORK_LIMIT", "count": work.QualityReworkCount}}); err != nil {
			return err
		}
	}
	return m.recomputeResultDependantsOn(ctx, tx, work, actor, at)
}
func (m *Module) newReworkRequirementOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID, draft intake.TriageDraft) (guid.ID, error) {
	if err := m.reworkDispositionAuthorityOn(ctx, tx, tenant, project); err != nil {
		return 0, err
	}
	if m.sources.GetOn == nil {
		return 0, ErrUnavailable
	}
	fact, found, err := m.sources.GetOn(ctx, tx, tenant, source)
	if err != nil {
		return 0, err
	}
	if !found || fact.Kind != "REWORK" || fact.ProjectID == nil || *fact.ProjectID != project || !slices.Contains([]string{"RECEIVED", "TRIAGED", "CONVERTED"}, fact.Status) {
		return 0, ErrResultState
	}
	criteria := make([]requirementCriterionInput, len(draft.Criteria))
	for i, text := range draft.Criteria {
		criteria[i] = requirementCriterionInput{Text: text, Required: true}
	}
	row, err := m.createRequirementOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), requirementCreateBody{Title: draft.Summary, Description: draft.RequirementDraft, Criteria: criteria})
	if err != nil {
		return 0, err
	}
	q := query.BindRequirements(tx, m.service.names)
	_, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(row.ID)).UpdateSimple(q.SourceIntakeID.Value(source))
	return row.ID, err
}

// DispatchStarvationFacts is system discovery; it conveys no assignment authority.
type DispatchStarvedWork struct {
	ProjectID, WorkItemID guid.ID
	Version               int
}
type DispatchStarvationFacts struct {
	ListOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]DispatchStarvedWork, error)
	CurrentOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error)
}

func (m *Module) dispatchStarvedListOn(ctx context.Context, tx *gorm.DB, tenant, after guid.ID, at time.Time) ([]DispatchStarvedWork, error) {
	if tx == nil || tenant <= 0 || after < 0 || at.IsZero() {
		return nil, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	p := query.BindProjects(tx, m.service.names)
	a := query.BindAssignments(tx, m.service.names)
	projects := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.EqCol(w.ProjectID), p.Stage.Eq("ACTIVE"), p.ArchivedAt.IsNull())
	assigned := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.EqCol(w.ProjectID), a.WorkItemID.EqCol(w.ID), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE"), a.EndedAt.IsNull()).Where(a.WithContext(ctx).Where(a.LeaseExpiresAt.IsNull()).Or(a.LeaseExpiresAt.Gt(at.UTC())))
	rows, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Gt(after), w.State.Eq("READY"), w.IsContainer.Is(false), w.PausedAt.IsNull(), w.ArchivedAt.IsNull(), w.StateChangedAt.Lt(at.UTC().Add(-4*time.Hour))).Where(gen.Exists(projects)).Not(gen.Exists(assigned)).Order(w.ID).Limit(100).Find()
	if err != nil {
		return nil, err
	}
	out := make([]DispatchStarvedWork, 0, len(rows))
	for _, row := range rows {
		out = append(out, DispatchStarvedWork{ProjectID: row.ProjectID, WorkItemID: row.ID, Version: row.Version})
	}
	return out, nil
}
func (m *Module) dispatchStarvedCurrentOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, at time.Time) (bool, error) {
	if work <= 0 || project <= 0 {
		return false, ErrInvalidArgument
	}
	rows, err := m.dispatchStarvedListOn(ctx, tx, tenant, work-1, at)
	if err != nil {
		return false, err
	}
	return len(rows) > 0 && rows[0].ProjectID == project && rows[0].WorkItemID == work, nil
}
