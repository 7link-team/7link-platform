package delivery

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrWorkForbidden       = errors.New("delivery: work item forbidden")
	ErrWorkProjectReadOnly = errors.New("delivery: project is read only")
)

type workCriterionInput struct {
	Text               string `json:"text" minLength:"1" maxLength:"2000"`
	VerificationMethod string `json:"verification_method" minLength:"1" maxLength:"2000"`
}

type workCreateBody struct {
	DefectDetails   *DefectDescription `json:"defect_details,omitempty"`
	Severity        *string            `json:"severity,omitempty" enum:"FATAL,CRITICAL,NORMAL,MINOR"`
	Priority        *string            `json:"priority,omitempty" enum:"URGENT,HIGH,MEDIUM,LOW"`
	Reproducibility *string            `json:"reproducibility,omitempty" enum:"ALWAYS,HIGH,MEDIUM,LOW,UNABLE"`
	FoundVersion    *string            `json:"found_version,omitempty" maxLength:"64"`

	Title                  string               `json:"title" minLength:"1" maxLength:"200"`
	Description            string               `json:"description" maxLength:"20000"`
	Type                   string               `json:"type" enum:"FEATURE,BUG,CHORE,DOC,RESEARCH,DESIGN,TEST,TASK,RISK,INCIDENT,CONTENT" default:"TASK"`
	Objective              string               `json:"objective" minLength:"1" maxLength:"2000"`
	AccountablePrincipalID guid.ID              `json:"accountable_principal_id" required:"true" doc:"Active Human tenant member responsible for this work."`
	RequirementID          *guid.ID             `json:"requirement_id,omitempty" doc:"Optional baselined Requirement in this instance."`
	BaselineVersionNo      *int                 `json:"baseline_version_no,omitempty" minimum:"1" maximum:"200" doc:"Required only when linking a Requirement; must match its current baseline."`
	Criteria               []workCriterionInput `json:"criteria" minItems:"1" maxItems:"50"`
}

type workCriterionView struct {
	ID                 guid.ID `json:"id"`
	Position           int     `json:"position"`
	Text               string  `json:"text"`
	VerificationMethod string  `json:"verification_method"`
}

type workContractSnapshot struct {
	Severity        *string `json:"severity,omitempty" enum:"FATAL,CRITICAL,NORMAL,MINOR"`
	Priority        *string `json:"priority,omitempty" enum:"URGENT,HIGH,MEDIUM,LOW"`
	Reproducibility *string `json:"reproducibility,omitempty" enum:"ALWAYS,HIGH,MEDIUM,LOW,UNABLE"`
	FoundVersion    *string `json:"found_version,omitempty" maxLength:"64"`

	Title                   string              `json:"title"`
	Description             string              `json:"description"`
	Type                    string              `json:"type"`
	Objective               string              `json:"objective"`
	AccountablePrincipalID  guid.ID             `json:"accountable_principal_id"`
	RequirementID           *guid.ID            `json:"requirement_id"`
	BaselineVersionNo       int                 `json:"baseline_version_no"`
	RequirementCriterionIDs []guid.ID           `json:"requirement_criterion_ids,omitempty"`
	Criteria                []workCriterionView `json:"criteria"`
}

type WorkView struct {
	ReopenedFromWorkItemID *guid.ID   `json:"reopened_from_work_item_id"`
	EscapedFromStage       *string    `json:"escaped_from_stage"`
	ActivitySeq            int64      `json:"activity_seq"`
	ScheduledStart         *time.Time `json:"scheduled_start"`
	ScheduledEnd           *time.Time `json:"scheduled_end"`
	ScheduleTimezone       string     `json:"schedule_timezone"`
	AndonActive            bool       `json:"andon_active,omitempty"`
	BaselineStale          bool       `json:"baseline_stale"`
	PauseReason            *string    `json:"pause_reason"`
	CurrentExecutionID     *guid.ID   `json:"current_execution_id"`
	DefectStage            *string    `json:"defect_stage"`
	CurrentClaimID         *guid.ID   `json:"current_claim_id"`
	AcceptedAt             *time.Time `json:"accepted_at"`
	AcceptedDecisionID     *guid.ID   `json:"accepted_decision_id"`
	Severity               *string    `json:"severity,omitempty" enum:"FATAL,CRITICAL,NORMAL,MINOR"`
	Priority               *string    `json:"priority,omitempty" enum:"URGENT,HIGH,MEDIUM,LOW"`
	Reproducibility        *string    `json:"reproducibility,omitempty" enum:"ALWAYS,HIGH,MEDIUM,LOW,UNABLE"`
	FoundVersion           *string    `json:"found_version,omitempty" maxLength:"64"`

	ID                     guid.ID    `json:"id"`
	ProjectID              guid.ID    `json:"project_id"`
	Number                 int        `json:"number"`
	Title                  string     `json:"title"`
	Description            string     `json:"description"`
	Type                   string     `json:"type"`
	State                  string     `json:"state"`
	IsContainer            bool       `json:"is_container"`
	PausedAt               *time.Time `json:"paused_at"`
	ArchivedAt             *time.Time `json:"archived_at"`
	ReadinessOverrideBy    *guid.ID   `json:"readiness_override_by"`
	Objective              *string    `json:"objective"`
	AccountablePrincipalID guid.ID    `json:"accountable_principal_id"`
	RequirementID          *guid.ID   `json:"requirement_id"`
	BaselineVersionNo      int        `json:"baseline_version_no"`
	Version                int        `json:"version"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type workContractView struct {
	VersionNo   int                  `json:"version_no"`
	Snapshot    workContractSnapshot `json:"snapshot"`
	ContentHash string               `json:"content_hash"`
	ConfirmedAt time.Time            `json:"confirmed_at"`
	ConfirmedBy guid.ID              `json:"confirmed_by"`
}

type workDetailView struct {
	StateKey string `json:"state_key"`
	WorkView
	Preparation workReadinessView   `json:"preparation"`
	Criteria    []workCriterionView `json:"criteria"`
	Contract    *workContractView   `json:"contract,omitempty" doc:"Absent for legacy records without a confirmed contract."`
}

func workRecord(row *model.WorkItem) WorkView {
	return WorkView{ReopenedFromWorkItemID: row.ReopenedFromWorkItemID, EscapedFromStage: row.EscapedFromStage, ActivitySeq: row.ActivitySeq, ScheduledStart: row.ScheduledStart, ScheduledEnd: row.ScheduledEnd, ScheduleTimezone: row.ScheduleTimezone, AndonActive: andonActive(row), BaselineStale: row.BaselineStale, PauseReason: row.PauseReason, CurrentExecutionID: row.CurrentExecutionID, DefectStage: row.DefectStage, CurrentClaimID: row.CurrentClaimID, AcceptedAt: row.AcceptedAt, AcceptedDecisionID: row.AcceptedDecisionID, Severity: row.Severity, Priority: row.Priority, Reproducibility: row.Reproducibility, FoundVersion: row.FoundVersion, ID: row.ID, ProjectID: row.ProjectID, Number: row.Number, Title: row.Title, Description: row.Description, Type: row.Type, State: row.State, IsContainer: row.IsContainer, PausedAt: row.PausedAt, ArchivedAt: row.ArchivedAt, ReadinessOverrideBy: row.ReadinessOverrideBy, Objective: row.Objective, AccountablePrincipalID: row.AccountablePrincipalID, RequirementID: row.RequirementID, BaselineVersionNo: row.BaselineVersionNo, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func validateWork(input workCreateBody) (workCreateBody, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Objective = strings.TrimSpace(input.Objective)
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 200 || input.Objective == "" || utf8.RuneCountInString(input.Objective) > 2000 || utf8.RuneCountInString(input.Description) > 20000 || input.AccountablePrincipalID <= 0 || len(input.Criteria) < 1 || len(input.Criteria) > 50 {
		return input, ErrInvalidArgument
	}
	if !slices.Contains([]string{"FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST", "TASK", "RISK", "INCIDENT", "CONTENT"}, input.Type) {
		return input, ErrInvalidArgument
	}
	if (input.RequirementID == nil) != (input.BaselineVersionNo == nil) || (input.RequirementID != nil && (*input.RequirementID <= 0 || *input.BaselineVersionNo < 1 || *input.BaselineVersionNo > 200)) {
		return input, ErrInvalidArgument
	}
	defect := input.Type == "BUG" || input.Type == "INCIDENT"
	if defect {
		if input.DefectDetails != nil {
			if err := input.DefectDetails.validate(); err != nil {
				return input, err
			}
		}
		if input.Severity == nil || input.Priority == nil || input.Reproducibility == nil || !slices.Contains([]string{"FATAL", "CRITICAL", "NORMAL", "MINOR"}, *input.Severity) || !slices.Contains([]string{"URGENT", "HIGH", "MEDIUM", "LOW"}, *input.Priority) || !slices.Contains([]string{"ALWAYS", "HIGH", "MEDIUM", "LOW", "UNABLE"}, *input.Reproducibility) {
			return input, ErrInvalidArgument
		}
	} else if input.Severity != nil || input.Priority != nil || input.Reproducibility != nil || input.FoundVersion != nil || input.DefectDetails != nil {
		return input, ErrInvalidArgument
	}
	if input.FoundVersion != nil && (!utf8.ValidString(*input.FoundVersion) || utf8.RuneCountInString(*input.FoundVersion) > 64) {
		return input, ErrInvalidArgument
	}
	input.Criteria = slices.Clone(input.Criteria)
	for i := range input.Criteria {
		c := &input.Criteria[i]
		c.Text = strings.TrimSpace(c.Text)
		c.VerificationMethod = strings.TrimSpace(c.VerificationMethod)
		if c.Text == "" || c.VerificationMethod == "" || utf8.RuneCountInString(c.Text) > 2000 || utf8.RuneCountInString(c.VerificationMethod) > 2000 {
			return input, ErrInvalidArgument
		}
	}
	return input, nil
}

// workScope applies the same IAM entity/owner constraint before count, paging
// and detail lookup. ResourceIDs denote entities, never task IDs.
func workScope(c authz.DataConstraint, entity guid.ID, owner field.Field) (field.Expr, error) {
	if !c.AllowsEntityRows(entity) {
		return field.NewInt("", "id").Eq(0), nil
	}
	if c.AllowAll || slices.Contains(c.ResourceIDs, entity.String()) {
		return nil, nil
	}
	ids := make([]driver.Valuer, 0, len(c.OwnerIDs))
	for _, raw := range c.OwnerIDs {
		id, err := guid.RequireID(raw)
		if err != nil {
			return nil, ErrUnavailable
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return field.NewInt("", "id").Eq(0), nil
	}
	return owner.In(ids...), nil
}

// createWorkOn is called only inside the idempotency savepoint. Principal locks
// precede the IAM policy fence; all business rows and evidence commit together.
func (m *Module) createWorkOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, input workCreateBody) (workDetailView, error) {
	return m.createWorkWithActorOn(ctx, tx, tenant, entity, actor, input, false)
}

// Only the artifact-comment conversion command enables an internal Agent actor.
// The accountable principal and all ordinary work creation entry points retain
// their canonical active-Human checks.
func (m *Module) createWorkWithActorOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, input workCreateBody, allowAgent bool) (workDetailView, error) {
	return m.createWorkActorOn(ctx, ctx, tx, tenant, entity, actor, input, allowAgent, nil)
}

// Automation creates an unconfirmed draft. The source contract and criteria
// are copied as proposed content; a SYSTEM run cannot assert Human confirmation.
func (m *Module) createWorkActorOn(ctx, writeCtx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, input workCreateBody, allowAgent bool, automation *AutomationTransition) (workDetailView, error) {
	var empty workDetailView
	input, err := validateWork(input)
	if err != nil {
		return empty, err
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || entity <= 0 || actor <= 0 || authn.PrincipalIDFromContext(ctx) != actor {
		return empty, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, ErrInvalidArgument
	}
	if m.workPolicy == nil || m.ids == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.service.audit == nil || m.service.events == nil {
		return empty, ErrUnavailable
	}
	ids := []guid.ID{actor, input.AccountablePrincipalID}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if err := automationPrincipalLockFence(ctx, ids, actor); err != nil {
		return empty, err
	}
	for _, id := range ids {
		if err = m.principals.Lock(ctx, tx, id); err != nil {
			return empty, err
		}
	}
	c, err := m.workPolicy(ctx, tx, tenant, entity, "work_item_write", actor)
	if err != nil {
		return empty, err
	}
	_, err = workScope(c, entity, field.NewField("", "created_by"))
	if err != nil {
		return empty, err
	}
	if !c.AllowsEntityRows(entity) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, entity.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
		return empty, ErrWorkForbidden
	}
	actorKind := "HUMAN"
	for _, id := range ids {
		human, active, e := m.principals.ActiveHumanByID(ctx, tx, id)
		if e != nil {
			return empty, e
		}
		if !active || human.ID != id {
			if !allowAgent || id != actor || id == input.AccountablePrincipalID || m.agents.ByPrincipalOn == nil {
				return empty, ErrInvalidMember
			}
			agent, found, agentErr := m.agents.ByPrincipalOn(ctx, tx, tenant, id)
			if agentErr != nil {
				return empty, agentErr
			}
			if !found || agent.TenantID != tenant || agent.PrincipalID != id || agent.State != "ACTIVE" {
				return empty, ErrInvalidMember
			}
			actorKind = "AGENT"
		}
		active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, id)
		if e != nil {
			return empty, e
		}
		if !active {
			return empty, ErrInvalidMember
		}
	}
	p := query.BindProjects(tx, m.service.names)
	// A project lock serializes number allocation, including legacy projects
	// whose counters have not yet been initialized.
	project, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(entity), p.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	if slices.Contains([]string{"CLOSED", "CANCELLED"}, project.Stage) { //nolint:misspell // Frozen business state.
		return empty, ErrWorkProjectReadOnly
	}
	baseline := 0
	if input.RequirementID != nil {
		r := query.BindRequirements(tx, m.service.names)
		row, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ID.Eq(*input.RequirementID), r.ArchivedAt.IsNull(), r.Status.Eq("BASELINED")).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return empty, ErrInvalidArgument
		}
		if e != nil {
			return empty, e
		}
		if row.CurrentVersionNo != *input.BaselineVersionNo {
			return empty, ErrInvalidArgument
		}
		v := query.BindRequirementVersions(tx, m.service.names)
		n, e := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ProjectID.Eq(entity), v.RequirementID.Eq(*input.RequirementID), v.VersionNo.Eq(*input.BaselineVersionNo)).Count()
		if e != nil {
			return empty, e
		}
		if n != 1 {
			return empty, ErrInvalidArgument
		}
		baseline = *input.BaselineVersionNo
	}
	writer := actor
	if automation != nil {
		writer = 0
	}
	number, err := m.nextWorkNumberOn(writeCtx, tx, tenant, entity, writer)
	if err != nil {
		return empty, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.WorkItem{Severity: input.Severity, Priority: input.Priority, Reproducibility: input.Reproducibility, FoundVersion: input.FoundVersion, TenantID: tenant, ID: guid.ID(id), ProjectID: entity, RequirementID: input.RequirementID, Number: number, Title: input.Title, Description: input.Description, Type: input.Type, Risk: "MEDIUM", State: "DRAFT", BlockReasons: "[]", SkillTags: "[]", PreferredSkillTags: "[]", BaselineVersionNo: baseline, StateChangedAt: now, AccountablePrincipalID: input.AccountablePrincipalID, AutonomyLevel: "MEDIUM", Objective: &input.Objective, OutOfScope: "[]", ContextRefs: "[]", VerificationPlan: "[]", ReadinessOK: false, ReadinessIssues: "[]", InternalQARequired: true, Tolerance: `{"time_pct":20,"cost_pct":20,"scope":"NO_NEW_CRITERIA"}`, CreatedAt: now, CreatedBy: &actor, UpdatedAt: now, UpdatedBy: &actor, Version: 1}
	if automation != nil {
		row.CreatedBy, row.UpdatedBy = nil, nil
	}
	if input.DefectDetails != nil {
		if input.DefectDetails.DuplicateOfWorkItemID != nil {
			if _, e := m.readDefectOn(ctx, tx, entity, *input.DefectDetails.DuplicateOfWorkItemID); e != nil {
				return empty, e
			}
		}
		raw, e := json.Marshal(input.DefectDetails)
		if e != nil {
			return empty, e
		}
		value := string(raw)
		row.DefectDetails = &value
	}
	w := query.BindWorkItems(tx, m.service.names)
	if err = w.WithContext(writeCtx).Create(row); err != nil {
		return empty, err
	}
	criteria := make([]workCriterionView, 0, len(input.Criteria))
	cq := query.BindWorkCriteria(tx, m.service.names)
	for i, item := range input.Criteria {
		cid, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		clause := &model.WorkItemCriterion{TenantID: tenant, ID: guid.ID(cid), ProjectID: entity, WorkItemID: guid.ID(id), Position: i, Text: item.Text, VerificationMethod: item.VerificationMethod, CreatedAt: now, CreatedBy: writer}
		if e = cq.WithContext(writeCtx).Create(clause); e != nil {
			return empty, e
		}
		criteria = append(criteria, workCriterionView{ID: guid.ID(cid), Position: i, Text: item.Text, VerificationMethod: item.VerificationMethod})
	}
	if automation != nil {
		detail := map[string]any{"actor_kind": "SYSTEM", "authority_source": automation.RunAs, "rule_id": automation.RuleID, "run_id": automation.RunID, "creator_id": automation.CreatorID, "source_work_item_id": automation.WorkItemID, "project_id": entity, "number": number, "contract_version_no": 0, "contract_confirmed": false}
		if err = m.service.audit(writeCtx, tx, audit.DomainEvent{TenantID: tenant, EventType: "delivery.work_item_created", TargetType: "work_item", TargetID: row.ID, Detail: detail}); err != nil {
			return empty, err
		}
		if _, err = m.service.events.Publish(writeCtx, tx, events.Event{TenantID: tenant, ProjectID: entity, Type: "work_item.created", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: automation.RunAs}, Subject: events.Subject{Kind: "work_item", ID: row.ID}, SubjectVersion: 1, Data: detail}); err != nil {
			return empty, err
		}
		preparation, err := preparationRecord(row)
		key, keyErr := m.workDisplayKeyOn(ctx, tx, row)
		if keyErr != nil {
			return workDetailView{}, keyErr
		}
		return workDetailView{StateKey: key, WorkView: workRecord(row), Preparation: preparation, Criteria: criteria}, err
	}
	snapshot := workContractSnapshot{Severity: row.Severity, Priority: row.Priority, Reproducibility: row.Reproducibility, FoundVersion: row.FoundVersion, Title: row.Title, Description: row.Description, Type: row.Type, Objective: input.Objective, AccountablePrincipalID: row.AccountablePrincipalID, RequirementID: row.RequirementID, BaselineVersionNo: baseline, Criteria: criteria}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return empty, err
	}
	hash := sha256.Sum256(encoded)
	contractID, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	contract := &model.WorkItemContract{TenantID: tenant, ID: guid.ID(contractID), ProjectID: entity, WorkItemID: guid.ID(id), VersionNo: 1, Snapshot: string(encoded), ContentHash: hex.EncodeToString(hash[:]), ConfirmedAt: now, ConfirmedBy: actor}
	if err = query.BindWorkContracts(tx, m.service.names).WithContext(ctx).Create(contract); err != nil {
		return empty, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.work_item_created", "work_item", guid.ID(id))
	event.Detail["project_id"] = entity.String()
	event.Detail["contract_hash"] = contract.ContentHash
	event.Detail["policy_revision"] = c.Revision
	if err = m.service.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: entity, Type: "work_item.created", Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: "permission:work_item_write"}, Subject: events.Subject{Kind: "work_item", ID: guid.ID(id)}, SubjectVersion: 1, Data: map[string]any{"number": number, "contract_version_no": 1, "content_hash": contract.ContentHash}})
	if err != nil {
		return empty, err
	}
	preparation, err := preparationRecord(row)
	if err != nil {
		return empty, err
	}
	key, keyErr := m.workDisplayKeyOn(ctx, tx, row)
	if keyErr != nil {
		return workDetailView{}, keyErr
	}
	return workDetailView{StateKey: key, WorkView: workRecord(row), Preparation: preparation, Criteria: criteria, Contract: &workContractView{VersionNo: 1, Snapshot: snapshot, ContentHash: contract.ContentHash, ConfirmedAt: now, ConfirmedBy: actor}}, nil
}

func (m *Module) nextWorkNumberOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID) (int, error) {
	return m.nextProjectNumberOn(ctx, tx, tenant, entity, actor, false)
}

// Called under the canonical current-Human/principal/project/work fence. A
// preparation save does not imply confirmation: the caller must opt in.
func (m *Module) confirmDraftWorkContractOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, actor guid.ID) error {
	if !slices.Contains([]string{"DRAFT", "BLOCKED"}, work.State) || work.CurrentExecutionID != nil || work.PausedAt != nil || actor <= 0 || authn.PrincipalIDFromContext(ctx) != actor || work.Objective == nil {
		return ErrWorkPreparationState
	}
	q := query.BindWorkContracts(tx, m.service.names)
	_, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID)).First()
	if err == nil {
		return ErrWorkVersion
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	cq := query.BindWorkCriteria(tx, m.service.names)
	rows, err := cq.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(cq.TenantID.Eq(work.TenantID), cq.ProjectID.Eq(work.ProjectID), cq.WorkItemID.Eq(work.ID)).Order(cq.Position).Find()
	if err != nil {
		return err
	}
	snapshot := workContractSnapshot{Severity: work.Severity, Priority: work.Priority, Reproducibility: work.Reproducibility, FoundVersion: work.FoundVersion, Title: work.Title, Description: work.Description, Type: work.Type, Objective: *work.Objective, AccountablePrincipalID: work.AccountablePrincipalID, RequirementID: work.RequirementID, BaselineVersionNo: work.BaselineVersionNo}
	input := workCreateBody{Severity: work.Severity, Priority: work.Priority, Reproducibility: work.Reproducibility, FoundVersion: work.FoundVersion, Title: work.Title, Description: work.Description, Type: work.Type, Objective: *work.Objective, AccountablePrincipalID: work.AccountablePrincipalID, RequirementID: work.RequirementID}
	if work.RequirementID != nil {
		baseline := work.BaselineVersionNo
		input.BaselineVersionNo = &baseline
		r := query.BindRequirements(tx, m.service.names)
		requirement, err := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(work.TenantID), r.ProjectID.Eq(work.ProjectID), r.ID.Eq(*work.RequirementID), r.ArchivedAt.IsNull()).First()
		if err != nil {
			return err
		}
		if requirement.Status != "BASELINED" || requirement.CurrentVersionNo != baseline || work.BaselineStale {
			return ErrRequirementVersion
		}
	}
	for _, c := range rows {
		snapshot.Criteria = append(snapshot.Criteria, workCriterionView{ID: c.ID, Position: c.Position, Text: c.Text, VerificationMethod: c.VerificationMethod})
		input.Criteria = append(input.Criteria, workCriterionInput{Text: c.Text, VerificationMethod: c.VerificationMethod})
	}
	if _, err = validateWork(input); err != nil {
		return err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(encoded)
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	contract := &model.WorkItemContract{TenantID: work.TenantID, ID: guid.ID(id), ProjectID: work.ProjectID, WorkItemID: work.ID, VersionNo: 1, Snapshot: string(encoded), ContentHash: hex.EncodeToString(sum[:]), ConfirmedAt: now, ConfirmedBy: actor}
	if err = q.WithContext(ctx).Create(contract); err != nil {
		return err
	}
	event := audit.NewDomainEvent(ctx, work.TenantID, "delivery.work_contract_confirmed", "work_item", work.ID)
	event.Detail["contract_hash"], event.Detail["contract_version_no"] = contract.ContentHash, 1
	return m.service.audit(ctx, tx, event)
}

func (m *Module) nextProjectNumberOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, requirement bool) (int, error) {
	c := query.BindCounters(tx, m.service.names)
	row, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(entity)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		w := query.BindWorkItems(tx, m.service.names)
		r := query.BindRequirements(tx, m.service.names)
		var maxWork, maxReq int
		if err = w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity)).Select(w.Number.Max().IfNull(0)).Scan(&maxWork); err != nil {
			return 0, err
		}
		if err = r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity)).Select(r.Number.Max().IfNull(0)).Scan(&maxReq); err != nil {
			return 0, err
		}
		id, e := m.ids.Next()
		if e != nil {
			return 0, e
		}
		now := time.Now().UTC()
		row = &model.ProjectCounter{TenantID: tenant, ID: guid.ID(id), ProjectID: entity, NextRequirementNo: maxReq + 1, NextWorkItemNo: maxWork + 1, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor}
		if actor == 0 {
			row.CreatedBy = nil
		}
		if e = c.WithContext(ctx).Create(row); e != nil {
			return 0, e
		}
	} else if err != nil {
		return 0, err
	}
	number := row.NextWorkItemNo
	if requirement {
		number = row.NextRequirementNo
		_, err = c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(row.ID)).UpdateSimple(c.NextRequirementNo.Value(number+1), c.UpdatedAt.Value(time.Now().UTC()))
		return number, err
	}
	_, err = c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(row.ID)).UpdateSimple(c.NextWorkItemNo.Value(number+1), c.UpdatedAt.Value(time.Now().UTC()))
	return number, err
}
