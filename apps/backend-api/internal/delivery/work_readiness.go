package delivery

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrWorkVersion          = errors.New("delivery: work version conflict")
	ErrWorkPreparationState = errors.New("delivery: work preparation is not editable in this state")
)

type workContextRef struct {
	Kind string `json:"kind" enum:"URL,WORK_ITEM,KNOWLEDGE"`
	Ref  string `json:"ref" minLength:"1" maxLength:"2000"`
}
type workVerificationStep struct {
	Argv        []string `json:"argv,omitempty" maxItems:"64"`
	TimeoutSec  int      `json:"timeout_sec,omitempty" minimum:"0" maximum:"3600"`
	ReportPath  string   `json:"report_path,omitempty" maxLength:"512"`
	CriterionID guid.ID  `json:"criterion_id"`
	Method      string   `json:"method" enum:"TEST,COMMAND,AGENT_JUDGE,HUMAN,VISUAL"`
	Detail      string   `json:"detail" minLength:"1" maxLength:"2000"`
}

// Decimal strings preserve exact optional token and money limits in browsers.
type workBudget struct {
	Tokens            *string `json:"tokens,omitempty" pattern:"^[0-9]+$" maxLength:"19"`
	USDMicros         *string `json:"usd_micros,omitempty" pattern:"^[0-9]+$" maxLength:"19"`
	WallClockMinutes  *int    `json:"wall_clock_minutes,omitempty" minimum:"1" maximum:"2147483647"`
	MaxInternalRounds int     `json:"max_internal_rounds" minimum:"1" maximum:"32767" default:"5"`
}
type WorkPreparationBody struct {
	ConfirmContract     bool                   `json:"confirm_contract,omitempty" doc:"Explicitly confirm the current objective and criteria of an unconfirmed draft as the current Human; never inferred from saving preparation."`
	EstimateMinutes     *int                   `json:"estimate_minutes,omitempty" minimum:"15" maximum:"4800"`
	Risk                string                 `json:"risk" enum:"LOW,MEDIUM,HIGH"`
	AutonomyLevel       string                 `json:"autonomy_level" enum:"LOW,MEDIUM,HIGH"`
	OutOfScope          []string               `json:"out_of_scope" maxItems:"20"`
	OutOfScopeConfirmed bool                   `json:"out_of_scope_confirmed"`
	ContextRefs         []workContextRef       `json:"context_refs" maxItems:"50"`
	VerificationPlan    []workVerificationStep `json:"verification_plan" maxItems:"50"`
	Budget              *workBudget            `json:"budget,omitempty" doc:"Optional preparation budget; omission clears a previous value."`
}
type workReadinessIssue struct {
	Code        string   `json:"code"`
	Field       string   `json:"field,omitempty"`
	CriterionID *guid.ID `json:"criterion_id,omitempty"`
	MessageKey  string   `json:"message_key"`
}
type workReadinessView struct {
	WorkPreparationBody
	OutOfScopeConfirmedAt *time.Time           `json:"out_of_scope_confirmed_at"`
	OK                    bool                 `json:"ok"`
	Issues                []workReadinessIssue `json:"issues"`
	BlockReasons          []string             `json:"block_reasons"`
	LastCheck             *workCheckView       `json:"last_check,omitempty"`
}
type workCheckView struct {
	ID              guid.ID              `json:"id"`
	WorkItemID      guid.ID              `json:"work_item_id"`
	WorkItemVersion int                  `json:"work_item_version"`
	Trigger         string               `json:"trigger"`
	OK              bool                 `json:"ok"`
	Issues          []workReadinessIssue `json:"issues"`
	CheckedBy       string               `json:"checked_by"`
	CreatedAt       time.Time            `json:"created_at"`
}
type workPreparationReceipt struct {
	ID           guid.ID       `json:"id"`
	Version      int           `json:"version"`
	State        string        `json:"state"`
	Check        workCheckView `json:"check"`
	BlockReasons []string      `json:"block_reasons"`
}

func validatePreparation(in WorkPreparationBody) (WorkPreparationBody, error) {
	if !slices.Contains([]string{"LOW", "MEDIUM", "HIGH"}, in.Risk) || !slices.Contains([]string{"LOW", "MEDIUM", "HIGH"}, in.AutonomyLevel) || (in.EstimateMinutes != nil && (*in.EstimateMinutes < 15 || *in.EstimateMinutes > 4800)) || len(in.OutOfScope) > 20 || len(in.ContextRefs) > 50 || len(in.VerificationPlan) > 50 {
		return in, ErrInvalidArgument
	}
	in.OutOfScope = slices.Clone(in.OutOfScope)
	for i, text := range in.OutOfScope {
		text = strings.TrimSpace(text)
		if text == "" || utf8.RuneCountInString(text) > 2000 {
			return in, ErrInvalidArgument
		}
		in.OutOfScope[i] = text
	}
	in.ContextRefs = slices.Clone(in.ContextRefs)
	for i, ref := range in.ContextRefs {
		ref.Ref = strings.TrimSpace(ref.Ref)
		if utf8.RuneCountInString(ref.Ref) > 2000 {
			return in, ErrInvalidArgument
		}
		switch ref.Kind {
		case "URL":
			u, err := url.Parse(ref.Ref)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
				return in, ErrInvalidArgument
			}
		case "WORK_ITEM", "KNOWLEDGE":
			if _, err := guid.RequireID(ref.Ref); err != nil {
				return in, ErrInvalidArgument
			}
		default:
			return in, ErrInvalidArgument
		}
		in.ContextRefs[i] = ref
	}
	in.VerificationPlan = slices.Clone(in.VerificationPlan)
	seen := make(map[guid.ID]bool)
	for i, step := range in.VerificationPlan {
		step.Detail = strings.TrimSpace(step.Detail)
		if step.CriterionID <= 0 || seen[step.CriterionID] || !slices.Contains([]string{"TEST", "COMMAND", "AGENT_JUDGE", "HUMAN", "VISUAL"}, step.Method) || step.Detail == "" || utf8.RuneCountInString(step.Detail) > 2000 {
			return in, ErrInvalidArgument
		}
		if len(step.Argv) > 64 || step.TimeoutSec < 0 || step.TimeoutSec > 3600 || len(step.ReportPath) > 512 {
			return in, ErrInvalidArgument
		}
		for _, arg := range step.Argv {
			if arg == "" || len(arg) > 8192 || strings.ContainsRune(arg, 0) {
				return in, ErrInvalidArgument
			}
		}
		seen[step.CriterionID] = true
		in.VerificationPlan[i] = step
	}
	if in.Budget != nil {
		b := *in.Budget
		if b.MaxInternalRounds == 0 {
			b.MaxInternalRounds = 5
		}
		if b.MaxInternalRounds < 1 || b.MaxInternalRounds > 32767 || (b.WallClockMinutes != nil && (*b.WallClockMinutes < 1 || *b.WallClockMinutes > 2147483647)) {
			return in, ErrInvalidArgument
		}
		for _, amount := range []*string{b.Tokens, b.USDMicros} {
			if amount != nil {
				n, err := strconv.ParseInt(*amount, 10, 64)
				if err != nil || n < 0 || strconv.FormatInt(n, 10) != *amount {
					return in, ErrInvalidArgument
				}
			}
		}
		in.Budget = &b
	}
	return in, nil
}

func preparationRecord(row *model.WorkItem) (workReadinessView, error) {
	v := workReadinessView{WorkPreparationBody: WorkPreparationBody{EstimateMinutes: row.EstimateMinutes, Risk: row.Risk, AutonomyLevel: row.AutonomyLevel, OutOfScopeConfirmed: row.OutOfScopeConfirmedAt != nil}, OutOfScopeConfirmedAt: row.OutOfScopeConfirmedAt, OK: row.ReadinessOK}
	for _, part := range []struct {
		raw  string
		dest any
	}{{row.OutOfScope, &v.OutOfScope}, {row.ContextRefs, &v.ContextRefs}, {row.VerificationPlan, &v.VerificationPlan}, {row.ReadinessIssues, &v.Issues}, {row.BlockReasons, &v.BlockReasons}} {
		if err := json.Unmarshal([]byte(part.raw), part.dest); err != nil {
			return v, err
		}
	}
	if row.Budget != nil {
		if err := json.Unmarshal([]byte(*row.Budget), &v.Budget); err != nil {
			return v, err
		}
	}
	return v, nil
}

// preparationOwner discovers only the lock set, outside the business transaction.
// The locked work row must still match it; it never authorizes the command.
func (m *Module) preparationOwner(ctx context.Context, db *gorm.DB, tenant, entity, id guid.ID) (guid.ID, error) {
	if db == nil || db.Statement == nil {
		return 0, ErrUnavailable
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return 0, ErrInvalidArgument
	}
	w := query.BindWorkItems(db, m.service.names)
	row, err := w.WithContext(ctx).Select(w.AccountablePrincipalID).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ID.Eq(id), w.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrProjectNotFound
	}
	if err != nil {
		return 0, err
	}
	return row.AccountablePrincipalID, nil
}

// lockedWorkOn follows principal -> IAM fence -> project -> work lock order.
// Each command applies its own state guards after these current locking reads.
func (m *Module) lockedWorkOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor, owner guid.ID, permission string) (*model.Project, *model.WorkItem, bool, error) {
	return m.lockedWorkActorOn(ctx, tx, tenant, entity, id, actor, owner, permission, false)
}

func (m *Module) lockedWorkActorOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor, owner guid.ID, permission string, allowAgent bool) (*model.Project, *model.WorkItem, bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || entity <= 0 || id <= 0 || actor <= 0 || authn.PrincipalIDFromContext(ctx) != actor {
		return nil, nil, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, nil, false, ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.workPolicy == nil || m.ids == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.service.audit == nil || m.service.events == nil {
		return nil, nil, false, ErrUnavailable
	}
	w := query.BindWorkItems(tx, m.service.names)
	principals := []guid.ID{actor, owner}
	hiddenPrincipals, err := m.hiddenAcceptancePrincipalsOn(ctx, tx, tenant, entity, id)
	if err != nil {
		return nil, nil, false, err
	}
	principals = append(principals, hiddenPrincipals...)
	slices.Sort(principals)
	principals = slices.Compact(principals)
	if err = automationPrincipalLockFence(ctx, principals, actor); err != nil {
		return nil, nil, false, err
	}
	for _, principal := range principals {
		if err := m.principals.Lock(ctx, tx, principal); err != nil {
			return nil, nil, false, err
		}
	}
	c, err := m.workPolicy(ctx, tx, tenant, entity, permission, actor)
	if err != nil {
		return nil, nil, false, err
	}
	scope, err := workScope(c, entity, w.CreatedBy)
	if err != nil {
		return nil, nil, false, err
	}
	ownerActive := false
	for _, principal := range principals {
		human, active, e := m.principals.ActiveHumanByID(ctx, tx, principal)
		if e != nil {
			return nil, nil, false, e
		}
		member, e := m.members.IsMemberActiveOn(ctx, tx, tenant, principal)
		if e != nil {
			return nil, nil, false, e
		}
		active = active && human.ID == principal && member
		if principal == actor && !active && member && allowAgent {
			if m.agents.ByPrincipalOn == nil {
				return nil, nil, false, ErrUnavailable
			}
			agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
			if e != nil {
				return nil, nil, false, e
			}
			active = found && agent.ID > 0 && agent.TenantID == tenant && agent.PrincipalID == actor && agent.State == "ACTIVE"
		}
		if principal == actor && !active {
			return nil, nil, false, ErrInvalidMember
		}
		if principal == owner {
			ownerActive = active
		}
	}
	p := query.BindProjects(tx, m.service.names)
	project, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(entity), p.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, false, ErrProjectNotFound
	}
	if err != nil {
		return nil, nil, false, err
	}
	if slices.Contains([]string{"CLOSED", "CANCELLED"}, project.Stage) { //nolint:misspell // Frozen business state.
		return nil, nil, false, ErrWorkProjectReadOnly
	}
	base := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ID.Eq(id), w.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	row, err := base.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, false, ErrProjectNotFound
	}
	if err != nil {
		return nil, nil, false, err
	}
	if row.AccountablePrincipalID != owner {
		return nil, nil, false, ErrWorkVersion
	}
	return project, row, ownerActive, nil
}

func (m *Module) preparationWorkOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor, owner guid.ID, permission string) (*model.WorkItem, bool, error) {
	_, row, ownerActive, err := m.lockedWorkOn(ctx, tx, tenant, entity, id, actor, owner, permission)
	if err != nil {
		return nil, false, err
	}
	if !slices.Contains([]string{"DRAFT", "READY", "BLOCKED"}, row.State) || row.CurrentClaimID != nil || row.CurrentExecutionID != nil {
		return nil, false, ErrWorkPreparationState
	}
	return row, ownerActive, nil
}

func (m *Module) prepareWorkOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor, owner guid.ID, expected int, input *WorkPreparationBody) (workPreparationReceipt, error) {
	return m.prepareWorkActorOn(ctx, tx, tenant, entity, id, actor, owner, expected, input, false)
}

// Existing preparation endpoints remain Human-only. The display transition
// command opts in only after its own current Agent authorization/context check.
func (m *Module) prepareWorkActorOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor, owner guid.ID, expected int, input *WorkPreparationBody, allowAgent bool) (workPreparationReceipt, error) {
	var empty workPreparationReceipt
	permission, trigger := "work_item_read", "MANUAL"
	if allowAgent {
		permission = "work_item_write"
	}
	if input != nil {
		permission, trigger = "work_item_write", "EDIT"
		valid, err := validatePreparation(*input)
		if err != nil {
			return empty, err
		}
		input = &valid
	}
	_, row, ownerActive, err := m.lockedWorkActorOn(ctx, tx, tenant, entity, id, actor, owner, permission, allowAgent)
	if err != nil {
		return empty, err
	}
	if !slices.Contains([]string{"DRAFT", "READY", "BLOCKED"}, row.State) || row.CurrentClaimID != nil || row.CurrentExecutionID != nil {
		return empty, ErrWorkPreparationState
	}
	actorKind := "HUMAN"
	if allowAgent {
		actorKind, err = m.resultActorKindOn(ctx, tx, actor, "")
		if err != nil {
			return empty, err
		}
	}
	if row.Version != expected {
		return empty, ErrWorkVersion
	}
	if input != nil && input.ConfirmContract {
		if err := m.confirmDraftWorkContractOn(ctx, tx, row, actor); err != nil {
			return empty, err
		}
	}
	before := row.State
	if input != nil {
		row.EstimateMinutes = input.EstimateMinutes
		row.Risk = input.Risk
		row.AutonomyLevel = input.AutonomyLevel
		// Values have already passed bounded, typed validation; encoding cannot fail.
		out, _ := json.Marshal(input.OutOfScope)
		refs, _ := json.Marshal(input.ContextRefs)
		plan, _ := json.Marshal(input.VerificationPlan)
		if input.OutOfScope == nil {
			out = []byte("[]")
		}
		if input.ContextRefs == nil {
			refs = []byte("[]")
		}
		if input.VerificationPlan == nil {
			plan = []byte("[]")
		}
		row.OutOfScope = string(out)
		row.ContextRefs = string(refs)
		row.VerificationPlan = string(plan)
		row.Budget = nil
		if input.Budget != nil {
			b, _ := json.Marshal(input.Budget)
			text := string(b)
			row.Budget = &text
		}
		row.OutOfScopeConfirmedAt = nil
		if input.OutOfScopeConfirmed {
			now := time.Now().UTC()
			row.OutOfScopeConfirmedAt = &now
		}
	}
	currentPreparation, err := preparationRecord(row)
	if err != nil {
		return empty, err
	}
	if _, err = validatePreparation(currentPreparation.WorkPreparationBody); err != nil {
		return empty, err
	}
	if err = m.validateContextOn(ctx, tx, tenant, entity, actor, currentPreparation.ContextRefs); err != nil {
		return empty, err
	}
	issues, blocks, err := m.evaluatePreparationOn(ctx, tx, row, ownerActive)
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC()
	row.ReadinessOK = len(issues) == 0
	encodedIssues, _ := json.Marshal(issues)
	encodedBlocks, _ := json.Marshal(blocks)
	row.ReadinessIssues = string(encodedIssues)
	row.BlockReasons = string(encodedBlocks)
	if row.PausedAt == nil {
		switch {
		case len(blocks) > 0:
			row.State = "BLOCKED"
		case !row.ReadinessOK:
			row.State = "DRAFT"
		default:
			row.State = "READY"
		}
	}
	row.Version++
	row.UpdatedAt = now
	row.UpdatedBy = &actor
	if before != row.State {
		row.StateChangedAt = now
	}
	row.PlanRequired = row.Risk != "LOW" || row.AutonomyLevel != "LOW"
	w := query.BindWorkItems(tx, m.service.names)
	result, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ID.Eq(id), w.Version.Eq(expected)).Select(w.EstimateMinutes, w.Risk, w.AutonomyLevel, w.OutOfScope, w.OutOfScopeConfirmedAt, w.ContextRefs, w.VerificationPlan, w.Budget, w.ReadinessOK, w.ReadinessIssues, w.BlockReasons, w.State, w.StateChangedAt, w.PlanRequired, w.Version, w.UpdatedAt, w.UpdatedBy).Updates(row)
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	checkID, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	check := &model.ReadinessCheck{TenantID: tenant, ID: guid.ID(checkID), ProjectID: entity, WorkItemID: id, WorkItemVersion: row.Version, Trigger: trigger, OK: row.ReadinessOK, Issues: row.ReadinessIssues, CheckedBy: "SYSTEM", CreatedAt: now}
	if err = query.BindReadinessChecks(tx, m.service.names).WithContext(ctx).Create(check); err != nil {
		return empty, err
	}
	transitionID, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	source := "readiness-check:" + check.ID.String()
	transition := &model.WorkItemTransition{TenantID: tenant, ID: guid.ID(transitionID), ProjectID: entity, WorkItemID: id, FromState: &before, ToState: row.State, Trigger: "COMMAND", ActorKind: actorKind, ActorID: &actor, AuthoritySource: "permission:" + permission, ReasonCode: "READINESS_CHECKED", SourceRef: &source, VersionAfter: row.Version, CreatedAt: now}
	if err = query.BindWorkTransitions(tx, m.service.names).WithContext(ctx).Create(transition); err != nil {
		return empty, err
	}
	auditEvent := audit.NewDomainEvent(ctx, tenant, "delivery.work_readiness_checked", "work_item", id)
	auditEvent.Detail["check_id"] = check.ID.String()
	auditEvent.Detail["ok"] = check.OK
	auditEvent.Detail["from"] = before
	auditEvent.Detail["to"] = row.State
	auditEvent.Detail["version"] = row.Version
	if input != nil && input.ConfirmContract {
		auditEvent.Detail["contract_confirmed"] = true
	}
	err = m.service.audit(ctx, tx, auditEvent)
	if err != nil {
		return empty, err
	}
	eventData := map[string]any{"check_id": check.ID.String(), "ok": check.OK, "issues": issues}
	event := events.Event{TenantID: tenant, ProjectID: entity, Type: "work_item.ready_checked", Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: "permission:" + permission}, Subject: events.Subject{Kind: "work_item", ID: id}, SubjectVersion: int64(row.Version), Data: eventData}
	if input != nil && input.ConfirmContract {
		eventData["contract_confirmed"] = true
	}
	if _, err = m.service.events.Publish(ctx, tx, event); err != nil {
		return empty, err
	}
	if before != row.State {
		event.Type = "work_item.state_changed"
		event.Data = map[string]any{"from": before, "to": row.State, "reason_code": "READINESS_CHECKED"}
		if _, err = m.service.events.Publish(ctx, tx, event); err != nil {
			return empty, err
		}
	}
	return workPreparationReceipt{ID: id, Version: row.Version, State: row.State, Check: workCheckView{ID: check.ID, WorkItemID: id, WorkItemVersion: row.Version, Trigger: trigger, OK: check.OK, Issues: issues, CheckedBy: check.CheckedBy, CreatedAt: now}, BlockReasons: blocks}, nil
}

func (m *Module) validateContextOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, refs []workContextRef) error {
	if err := m.validateKnowledgeRefsOn(ctx, tx, tenant, entity, refs); err != nil {
		return err
	}
	ids := make([]driver.Valuer, 0, len(refs))
	seen := make(map[guid.ID]bool)
	for _, ref := range refs {
		if ref.Kind == "WORK_ITEM" {
			id, err := guid.RequireID(ref.Ref)
			if err != nil {
				return ErrInvalidArgument
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	c, err := m.workPolicy(ctx, tx, tenant, entity, "work_item_read", actor)
	if err != nil {
		return err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, entity, w.CreatedBy)
	if err != nil {
		return err
	}
	base := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ID.In(ids...), w.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	rows, err := base.Select(w.ID).Find()
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return ErrProjectNotFound
	}
	return nil
}

func (m *Module) evaluatePreparationOn(ctx context.Context, tx *gorm.DB, row *model.WorkItem, ownerActive bool) ([]workReadinessIssue, []string, error) {
	v, err := preparationRecord(row)
	if err != nil {
		return nil, nil, err
	}
	issues := make([]workReadinessIssue, 0)
	blocks := make([]string, 0)
	add := func(code, field, key string, id *guid.ID) {
		issues = append(issues, workReadinessIssue{Code: code, Field: field, MessageKey: key, CriterionID: id})
	}
	missing := func(field string) { add("MISSING_FIELD", field, "delivery.readiness_missing_field", nil) }
	// Preserve actual quality findings; a deterministic field check cannot overrule them.
	for _, issue := range v.Issues {
		if slices.Contains([]string{"REQUIREMENT_CONFLICT", "REQUIREMENT_AMBIGUOUS", "UNVERIFIABLE_AC"}, issue.Code) && issue.MessageKey != "delivery.readiness_high_risk_method" {
			issues = append(issues, issue)
		}
	}
	if row.Objective == nil || strings.TrimSpace(*row.Objective) == "" {
		missing("objective")
	}
	if !ownerActive {
		add("NO_ACCOUNTABLE_HUMAN", "accountable_principal_id", "delivery.readiness_no_human", nil)
	}
	if row.OutOfScopeConfirmedAt == nil {
		missing("out_of_scope")
	}
	if len(v.ContextRefs) == 0 {
		missing("context_refs")
	}
	if v.Budget == nil {
		missing("budget")
	}
	if !slices.Contains([]string{"LOW", "MEDIUM", "HIGH"}, row.AutonomyLevel) {
		missing("autonomy_level")
	}
	c := query.BindWorkCriteria(tx, m.service.names)
	criteria, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(row.TenantID), c.ProjectID.Eq(row.ProjectID), c.WorkItemID.Eq(row.ID)).Order(c.Position).Limit(51).Find()
	if err != nil {
		return nil, nil, err
	}
	if len(criteria) == 0 {
		missing("acceptance_criteria")
	}
	if len(criteria) > 50 {
		return nil, nil, ErrUnavailable
	}
	contracts := query.BindWorkContracts(tx, m.service.names)
	contract, e := contracts.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(contracts.TenantID.Eq(row.TenantID), contracts.ProjectID.Eq(row.ProjectID), contracts.WorkItemID.Eq(row.ID)).Order(contracts.VersionNo.Desc()).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		missing("confirmed_contract")
	} else if e != nil {
		return nil, nil, e
	} else {
		var snapshot workContractSnapshot
		if e = json.Unmarshal([]byte(contract.Snapshot), &snapshot); e != nil {
			return nil, nil, e
		}
		encoded, e := json.Marshal(snapshot)
		if e != nil {
			return nil, nil, e
		}
		hash := sha256.Sum256(encoded)
		if hex.EncodeToString(hash[:]) != strings.TrimSpace(contract.ContentHash) || len(snapshot.Criteria) != len(criteria) || row.Objective == nil || snapshot.Objective != *row.Objective || snapshot.AccountablePrincipalID != row.AccountablePrincipalID {
			return nil, nil, ErrUnavailable
		}
		for i, criterion := range criteria {
			expected := snapshot.Criteria[i]
			if expected.ID != criterion.ID || expected.Position != criterion.Position || expected.Text != criterion.Text || expected.VerificationMethod != criterion.VerificationMethod {
				return nil, nil, ErrUnavailable
			}
		}
	}
	methods := make(map[guid.ID]workVerificationStep)
	validIDs := make(map[guid.ID]bool)
	for _, criterion := range criteria {
		validIDs[criterion.ID] = true
	}
	for _, step := range v.VerificationPlan {
		if !validIDs[step.CriterionID] || methods[step.CriterionID].CriterionID != 0 {
			return nil, nil, ErrInvalidArgument
		}
		methods[step.CriterionID] = step
	}
	for _, criterion := range criteria {
		step, ok := methods[criterion.ID]
		id := criterion.ID
		if !ok {
			add("AC_WITHOUT_METHOD", "verification_plan", "delivery.readiness_missing_method", &id)
		} else if row.Risk == "HIGH" && step.Method == "AGENT_JUDGE" {
			add("UNVERIFIABLE_AC", "verification_plan", "delivery.readiness_high_risk_method", &id)
		}
	}
	for _, reason := range v.BlockReasons {
		if !slices.Contains([]string{"ESTIMATE_MISSING", "DEPENDENCY", "DEPENDENCY_CANCELLED", "CHILDREN_OPEN", "REQUIREMENT_DRAFT", "NO_ACCOUNTABLE_HUMAN", "ANDON_UPSTREAM"}, reason) { //nolint:misspell // Frozen business state.
			blocks = append(blocks, reason)
		}
	}
	if row.EstimateMinutes == nil {
		blocks = append(blocks, "ESTIMATE_MISSING")
	}
	if !ownerActive {
		blocks = append(blocks, "NO_ACCOUNTABLE_HUMAN")
	}
	if row.IsContainer {
		blocks = append(blocks, "CHILDREN_OPEN")
	}
	if row.RequirementID != nil {
		r := query.BindRequirements(tx, m.service.names)
		requirement, e := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(row.TenantID), r.ProjectID.Eq(row.ProjectID), r.ID.Eq(*row.RequirementID), r.ArchivedAt.IsNull()).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			blocks = append(blocks, "REQUIREMENT_DRAFT")
		} else if e != nil {
			return nil, nil, e
		} else if requirement.Status != "BASELINED" || row.BaselineStale || requirement.CurrentVersionNo != row.BaselineVersionNo {
			blocks = append(blocks, "REQUIREMENT_DRAFT")
		}
	}
	d := query.BindDependencies(tx, m.service.names)
	dependencies, err := d.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(d.TenantID.Eq(row.TenantID), d.ProjectID.Eq(row.ProjectID), d.WorkItemID.Eq(row.ID)).Limit(51).Find()
	if err != nil {
		return nil, nil, err
	}
	if len(dependencies) > 50 {
		return nil, nil, ErrUnavailable
	}
	if len(dependencies) > 0 {
		ids := make([]driver.Valuer, 0, len(dependencies))
		for _, dep := range dependencies {
			ids = append(ids, dep.DependsOnID)
		}
		w := query.BindWorkItems(tx, m.service.names)
		predecessors, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select(w.ID, w.State, w.ArchivedAt).Where(w.TenantID.Eq(row.TenantID), w.ProjectID.Eq(row.ProjectID), w.ID.In(ids...)).Find()
		if e != nil {
			return nil, nil, e
		}
		if len(predecessors) != len(ids) {
			return nil, nil, ErrUnavailable
		}
		for _, predecessor := range predecessors {
			if predecessor.State == "CANCELLED" { //nolint:misspell // Frozen business state.
				blocks = append(blocks, "DEPENDENCY_CANCELLED") //nolint:misspell // Frozen business state.
			} else if predecessor.State != "ACCEPTED" {
				blocks = append(blocks, "DEPENDENCY")
			}
		}
	}

	if e := m.requireNoAndonOn(ctx, tx, row); errors.Is(e, ErrAndonBlocked) {
		blocks = append(blocks, "ANDON_UPSTREAM")
	} else if e != nil {
		return nil, nil, e
	}
	if e := m.requireCurrentUpstreamOn(ctx, tx, row); errors.Is(e, ErrUpstreamStale) {
		blocks = append(blocks, "UPSTREAM_STALE")
	} else if e != nil {
		return nil, nil, e
	}
	slices.Sort(blocks)
	blocks = slices.Compact(blocks)
	return issues, blocks, nil
}
