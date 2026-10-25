package yunxiao

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

type RuntimeFact struct {
	ExecutionID, ProjectID, WorkItemID, PrincipalID guid.ID
	StartedAt, FinishedAt                           time.Time
	CostMicros                                      *int64
}
type RuntimeReader func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (RuntimeFact, bool, error)
type RuntimeConnector struct{ BindExecution func(RuntimeReader) error }

func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.provideIntakeCallbacks(s); err != nil {
		return err
	}
	if err := module.Provide[IngressFailureFacts](s, m.ingressFailureOn); err != nil {
		return err
	}
	if err := m.providePlanningReports(s); err != nil {
		return err
	}
	if err := m.provideAutomation(s); err != nil {
		return err
	}
	if err := m.provideIntakeNotify(s); err != nil {
		return err
	}
	return module.Provide[RuntimeConnector](s, RuntimeConnector{BindExecution: func(read RuntimeReader) error {
		if read == nil || m.runtimeFacts != nil {
			return ErrInvalid
		}
		m.runtimeFacts = read
		return nil
	}})
}

var _ module.ServiceProvider = (*Module)(nil)

type WorklogInput struct {
	CalibrationReason string   `json:"calibration_reason,omitempty" maxLength:"500" doc:"Optional contextual participation basis, preserved in immutable history."`
	Timezone          *string  `json:"timezone,omitempty" maxLength:"64" doc:"IANA timezone for the Human work date; absent on legacy records."`
	WorkItemID        *guid.ID `json:"work_item_id,omitempty"`
	Category          string   `json:"category" enum:"DEVELOPMENT,TEST,REVIEW,DESIGN,DOC,MEETING,OPS,OTHER"`
	WorkDate          string   `json:"work_date" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	Minutes           int64    `json:"minutes" minimum:"1" maximum:"1440"`
	Note              string   `json:"note" maxLength:"500"`
}
type WorklogView struct {
	Timezone    *string    `json:"timezone"`
	ConfirmedBy *guid.ID   `json:"confirmed_by"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
	ID          guid.ID    `json:"id"`
	PrincipalID guid.ID    `json:"principal_id"`
	ActorKind   string     `json:"actor_kind"`
	Category    string     `json:"category"`
	WorkDate    string     `json:"work_date"`
	Minutes     int64      `json:"minutes"`
	CostMicros  *int64     `json:"cost_micros"`
	ExecutionID *guid.ID   `json:"execution_id,omitempty"`
	Note        string     `json:"note"`
	State       string     `json:"state"`
	Version     int        `json:"version"`
	WorkItemID  *guid.ID   `json:"work_item_id,omitempty"`
	ApprovedBy  *guid.ID   `json:"approved_by"`
	ApprovedAt  *time.Time `json:"approved_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func worklogView(r *model.Worklog) WorklogView {
	return WorklogView{Timezone: r.Timezone, ConfirmedBy: r.ConfirmedBy, ConfirmedAt: r.ConfirmedAt, ID: r.ID, PrincipalID: r.PrincipalID, ActorKind: r.ActorKind, Category: r.Category, WorkDate: r.WorkDate, Minutes: r.Minutes, CostMicros: r.CostMicros, ExecutionID: r.ExecutionID, Note: r.Note, State: r.State, Version: r.Version, WorkItemID: r.WorkItemID, ApprovedBy: r.ApprovedBy, ApprovedAt: r.ApprovedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// projectAuthorityOn locks current identity before current project policy. This
// also serializes all writes for one person's daily limit across projects.
func (m *Module) projectAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil || actor <= 0 || tenant <= 0 || project <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if m == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.projects.AuthorizeOn == nil {
		return ErrUnavailable
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return err
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active || human.ID != actor {
		return ErrForbidden
	}
	return m.projects.AuthorizeOn(ctx, tx, tenant, project, permission)
}
func (m *Module) workChangedOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, kind string, system bool, reason ...string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	if subject := kind; strings.HasPrefix(subject, "worklog.") {
		q := query.BindWorklogs(tx, m.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
		if e != nil {
			return e
		}
		var writer *guid.ID
		actorID := authn.PrincipalIDFromContext(ctx)
		if !system && actorID > 0 {
			writer = &actorID
		}
		why := ""
		if len(reason) > 0 {
			why = reason[0]
		}
		if e = m.recordWorklogRevisionOn(ctx, tx, row, kind, why, writer); e != nil {
			return e
		}
	}
	actor := events.Actor{Kind: "HUMAN", ID: authn.ActorIDFromContext(ctx), AuthoritySource: "IAM"}
	if system {
		actor = events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:execution_receipts"}
	}
	subjectKind := "worklog"
	if kind == "planning.entity_changed" {
		subjectKind = "planning_entity"
	}
	if kind == "planning.assignment_changed" {
		subjectKind = "planning_assignment"
	}
	if kind == "workflow.config_changed" {
		subjectKind = "workflow_config"
	}
	if err := m.audit(ctx, tx, audit.NewDomainEvent(ctx, tenant, kind, subjectKind, id)); err != nil {
		return err
	}
	data := map[string]any{"version": version}
	if len(reason) > 0 {
		data["reason"] = reason[0]
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: kind, Actor: actor, Subject: events.Subject{Kind: subjectKind, ID: id}, SubjectVersion: int64(version), Data: data})
	return err
}
func (m *Module) saveWorklogOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, in WorklogInput, correctionReason ...string) (WorklogView, error) {
	var out WorklogView
	if err := m.effortInputAuthorityOn(ctx, tx, tenant, project, in); err != nil {
		return out, err
	}
	if !textValid(in.CalibrationReason, 500) {
		return out, ErrInvalid
	}
	if id == 0 && len(correctionReason) == 0 && strings.TrimSpace(in.CalibrationReason) != "" {
		correctionReason = []string{in.CalibrationReason}
	}
	if in.Timezone != nil {
		if len(*in.Timezone) > 64 || strings.TrimSpace(*in.Timezone) == "" || *in.Timezone == "Local" {
			return out, ErrInvalid
		}
		if _, err := time.LoadLocation(*in.Timezone); err != nil {
			return out, ErrInvalid
		}
	}
	day, err := time.Parse("2006-01-02", in.WorkDate)
	if err != nil || day.Format("2006-01-02") != in.WorkDate || in.Minutes < 1 || in.Minutes > 1440 || !textValid(in.Note, 500) || !slices.Contains([]string{"DEVELOPMENT", "TEST", "REVIEW", "DESIGN", "DOC", "MEETING", "OPS", "OTHER"}, in.Category) {
		return out, ErrInvalid
	}
	actor := authn.PrincipalIDFromContext(ctx)
	q := query.BindWorklogs(tx, m.names)
	zeroCost := int64(0)
	row := &model.Worklog{CostMicros: &zeroCost, TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: "HUMAN", State: "DRAFT", Version: 1, CreatedAt: m.now()}
	if id > 0 {
		row, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
		if row.PrincipalID != actor {
			return out, ErrForbidden
		}
		if row.WorkItemID != nil && (in.WorkItemID == nil || *row.WorkItemID != *in.WorkItemID) {
			if m.effortWorkOn == nil {
				return out, ErrUnavailable
			}
			if err = m.effortWorkOn(ctx, tx, tenant, project, *row.WorkItemID); err != nil {
				return out, err
			}
		}
		correction := len(correctionReason) > 0 && strings.TrimSpace(correctionReason[0]) != "" && textValid(correctionReason[0], 500)
		if row.ExecutionID != nil || row.ActorKind != "HUMAN" || (!slices.Contains([]string{"DRAFT", "REJECTED"}, row.State) && !correction) || row.Version != version {
			return out, ErrConflict
		}
		if err = m.observeWorklogOn(ctx, tx, row); err != nil {
			return out, err
		}
		row.Version++
	} else {
		row.ID, err = m.nextID()
		if err != nil {
			return out, err
		}
	}
	// Include all human states, including rejected/draft, as required by the
	// single-person daily cap. Principal lock prevents cross-project write skew.
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(actor), q.WorkDate.Eq(in.WorkDate), q.ActorKind.Eq("HUMAN")).Find()
	if err != nil {
		return out, err
	}
	total := in.Minutes
	for _, r := range rows {
		if r.ID != row.ID {
			total += r.Minutes
		}
	}
	if total > 1440 {
		return out, ErrInvalid
	}
	row.ConfirmedBy, row.ConfirmedAt, row.ApprovedBy, row.ApprovedAt = nil, nil, nil, nil
	if in.Timezone != nil {
		row.Timezone = in.Timezone
	}
	row.Category = in.Category
	row.WorkItemID = in.WorkItemID
	row.WorkDate = in.WorkDate
	row.Minutes = in.Minutes
	row.Note = in.Note
	row.State = "DRAFT"
	row.UpdatedAt = m.now()
	if id == 0 {
		err = q.WithContext(ctx).Create(row)
	} else {
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Select(q.Timezone, q.ConfirmedBy, q.ConfirmedAt, q.ApprovedBy, q.ApprovedAt, q.WorkItemID, q.Category, q.WorkDate, q.Minutes, q.Note, q.State, q.Version, q.UpdatedAt).Updates(row)
		err = e
		if err == nil && result.RowsAffected != 1 {
			err = ErrConflict
		}
	}
	if err != nil {
		return out, err
	}
	if err = m.workChangedOn(ctx, tx, tenant, project, row.ID, row.Version, "worklog.saved", false, correctionReason...); err != nil {
		return out, err
	}
	return worklogView(row), nil
}
func (m *Module) worklogStateOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, state, reason string) (WorklogView, error) {
	var out WorklogView
	permission := "work_item_read"
	if err := m.projectAuthorityOn(ctx, tx, tenant, project, permission); err != nil {
		return out, err
	}
	q := query.BindWorklogs(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if row.Version != version || row.ExecutionID != nil {
		return out, ErrConflict
	}
	if row.WorkItemID != nil {
		if m.effortWorkOn == nil {
			return out, ErrUnavailable
		}
		if err = m.effortWorkOn(ctx, tx, tenant, project, *row.WorkItemID); err != nil {
			return out, err
		}
	}
	permission = "worklog_approve"
	if state == "SUBMITTED" && row.State == "DRAFT" {
		permission = "worklog_submit"
	}
	if err = m.projectAuthorityOn(ctx, tx, tenant, project, permission); err != nil {
		return out, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if err = m.observeWorklogOn(ctx, tx, row); err != nil {
		return out, err
	}
	kind := "worklog.submitted"
	switch {
	case state == "SUBMITTED" && row.State == "DRAFT":
		if actor != row.PrincipalID {
			return out, ErrForbidden
		}
	case state == "APPROVED" && row.State == "SUBMITTED":
		if actor == row.PrincipalID {
			return out, ErrForbidden
		}
		row.ApprovedBy = &actor
		now := m.now()
		row.ApprovedAt = &now
		kind = "worklog.approved"
	case state == "REJECTED" && row.State == "SUBMITTED":
		if actor == row.PrincipalID {
			return out, ErrForbidden
		}
		if reason == "" || !textValid(reason, 500) {
			return out, ErrInvalid
		}
		kind = "worklog.returned"
	case state == "SUBMITTED" && row.State == "APPROVED":
		if row.ApprovedBy == nil || *row.ApprovedBy != actor || actor == row.PrincipalID {
			return out, ErrForbidden
		}
		if err = m.projectAuthorityOn(ctx, tx, tenant, project, "worklog_approve"); err != nil {
			return out, err
		}
		if reason == "" || !textValid(reason, 500) {
			return out, ErrInvalid
		}
		row.ApprovedBy = nil
		row.ApprovedAt = nil
		kind = "worklog.withdrawn"
	default:
		return out, ErrConflict
	}
	row.State = state
	row.Version++
	row.UpdatedAt = m.now()
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Select(q.State, q.Version, q.ApprovedBy, q.ApprovedAt, q.UpdatedAt).Updates(row)
	if err != nil {
		return out, err
	}
	if result.RowsAffected != 1 {
		return out, ErrConflict
	}
	if err = m.workChangedOn(ctx, tx, tenant, project, id, row.Version, kind, false, reason); err != nil {
		return out, err
	}
	return worklogView(row), nil
}
func (m *Module) consumeRuntime(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	var data struct {
		State string `json:"state"`
	}
	if json.Unmarshal(e.Data, &data) != nil {
		return ErrInvalid
	}
	if !slices.Contains([]string{"SUCCEEDED", "FAILED", "CANCELLED", "INTERRUPTED"}, data.State) { //nolint:misspell // Frozen protocol state.
		return nil
	}
	if m.runtimeFacts == nil {
		return ErrUnavailable
	}
	fact, found, err := m.runtimeFacts(ctx, tx, e.TenantID, e.ProjectID, e.Subject.ID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if fact.ExecutionID != e.Subject.ID || fact.ProjectID != e.ProjectID {
		return ErrInvalid
	}
	return m.recordRuntimeOn(ctx, tx, e.TenantID, fact)
}
func (m *Module) recordRuntimeOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, f RuntimeFact) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || f.ExecutionID <= 0 || f.ProjectID <= 0 || f.PrincipalID <= 0 || f.StartedAt.IsZero() || !f.FinishedAt.After(f.StartedAt) || (f.CostMicros != nil && *f.CostMicros < 0) {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	q := query.BindWorklogs(tx, m.names)
	existing, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(f.ExecutionID)).Count()
	if err != nil || existing > 0 {
		return err
	}
	id, err := m.nextID()
	if err != nil {
		return err
	}
	now := m.now()
	// Nanosecond ceiling preserves subsecond receipts without floating point.
	minutes := int64((f.FinishedAt.Sub(f.StartedAt)-1)/time.Minute) + 1
	row := &model.Worklog{ID: id, TenantID: tenant, ProjectID: f.ProjectID, PrincipalID: f.PrincipalID, ActorKind: "AGENT", Category: "AGENT_RUNTIME", WorkDate: f.StartedAt.UTC().Format("2006-01-02"), Minutes: minutes, CostMicros: f.CostMicros, ExecutionID: &f.ExecutionID, State: "APPROVED", Version: 1, CreatedAt: now, UpdatedAt: now}
	if f.WorkItemID > 0 {
		row.WorkItemID = &f.WorkItemID
	}
	if err = q.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row); err != nil {
		return err
	}
	stored, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(f.ExecutionID)).First()
	if err != nil {
		return err
	}
	if stored.ID != id {
		return nil
	}
	return m.workChangedOn(ctx, tx, tenant, f.ProjectID, id, 1, "worklog.approved", true)
}
