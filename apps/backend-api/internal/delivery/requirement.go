package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrRequirementLimit   = errors.New("delivery: requirement limit exceeded")
	ErrCriterionLimit     = errors.New("delivery: active criterion limit exceeded")
	ErrRequirementVersion = errors.New("delivery: requirement version conflict")
	ErrRequirementFrozen  = errors.New("delivery: requirement already baselined")
	ErrRequirementStage   = errors.New("delivery: project stage forbids requirements")
)

type requirementCriterionInput struct {
	Text     string `json:"text" minLength:"1" maxLength:"2000"`
	Required bool   `json:"required"`
}
type requirementCreateBody struct {
	Title       string                      `json:"title" minLength:"1" maxLength:"200"`
	Description string                      `json:"description,omitempty" maxLength:"20000"`
	Priority    int                         `json:"priority,omitempty" minimum:"0" maximum:"3"`
	Criteria    []requirementCriterionInput `json:"criteria,omitempty" maxItems:"50"`
}

// RequirementView deliberately excludes persistence-only fields and description
// from list results. Concurrency version and baseline version are independent.
type RequirementView struct {
	ReviewState      string    `json:"review_state,omitempty" enum:"NEEDS_WORK,REVIEW_PENDING,APPROVED"`
	ID               guid.ID   `json:"id"`
	ProjectID        guid.ID   `json:"project_id"`
	Number           int       `json:"number"`
	Title            string    `json:"title"`
	Priority         int       `json:"priority"`
	Status           string    `json:"status"`
	CurrentVersionNo int       `json:"current_version_no"`
	Version          int       `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type requirementCriterionView struct {
	ID                  guid.ID `json:"id"`
	Code                string  `json:"code"`
	Text                string  `json:"text"`
	Required            bool    `json:"required"`
	Position            int     `json:"position"`
	IntroducedInVersion int     `json:"introduced_in_version"`
}
type requirementSnapshot struct {
	Title       string                     `json:"title"`
	Description string                     `json:"description"`
	Priority    int                        `json:"priority"`
	Criteria    []requirementCriterionView `json:"criteria"`
}
type requirementVersionView struct {
	SuiteHash      string              `json:"suite_hash,omitempty"`
	SuiteCaseCount int                 `json:"suite_case_count,omitempty"`
	VersionNo      int                 `json:"version_no"`
	Snapshot       requirementSnapshot `json:"snapshot"`
	ContentHash    string              `json:"content_hash"`
	CreatedAt      time.Time           `json:"created_at"`
	CreatedBy      *guid.ID            `json:"created_by"`
}
type requirementReviewSummary struct {
	SheetID guid.ID `json:"sheet_id"`
	State   string  `json:"state" enum:"OPEN,PASSED,REJECTED,CANCELLED"` //nolint:misspell // Frozen persisted protocol state.
}
type requirementDetailView struct {
	Decisions []RequirementReviewDecision `json:"decisions,omitempty"`
	Review    *requirementReviewSummary   `json:"review,omitempty"`
	RequirementView
	Description string                     `json:"description"`
	Criteria    []requirementCriterionView `json:"criteria"`
	Versions    []requirementVersionView   `json:"versions"`
}

func requirementRecord(r *model.Requirement) RequirementView {
	return RequirementView{ReviewState: requirementReviewState(r), ID: r.ID, ProjectID: r.ProjectID, Number: r.Number, Title: r.Title, Priority: r.Priority, Status: r.Status, CurrentVersionNo: r.CurrentVersionNo, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func requirementCriterionRecord(r *model.AcceptanceCriterion) requirementCriterionView {
	return requirementCriterionView{ID: r.ID, Code: r.Code, Text: r.Text, Required: r.Required, Position: r.Position, IntroducedInVersion: r.IntroducedInVersion}
}
func validateRequirement(in requirementCreateBody) (requirementCreateBody, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 200 || utf8.RuneCountInString(in.Description) > 20000 || in.Priority < 0 || in.Priority > 3 || len(in.Criteria) > 50 {
		return in, ErrInvalidArgument
	}
	in.Criteria = slices.Clone(in.Criteria)
	for i := range in.Criteria {
		in.Criteria[i].Text = strings.TrimSpace(in.Criteria[i].Text)
		if in.Criteria[i].Text == "" || utf8.RuneCountInString(in.Criteria[i].Text) > 2000 {
			return in, ErrInvalidArgument
		}
	}
	return in, nil
}

// Lock the acting Human before the existing IAM fence. Mutations never rely on
// a route-time authorization decision that could have changed before commit.
func (m *Module) requirementWriterOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID) (authz.DataConstraint, error) {
	return m.requirementWriterForStageOn(ctx, tx, tenant, entity, actor, false)
}

// Only the separately authorized versioned scope-change command may update a
// delivered baseline, making the required DELIVERED?ACTIVE decision possible.
func (m *Module) requirementWriterForStageOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, scopeChange bool) (authz.DataConstraint, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || entity <= 0 || actor <= 0 || authn.PrincipalIDFromContext(ctx) != actor {
		return authz.DataConstraint{}, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return authz.DataConstraint{}, ErrInvalidArgument
	}
	if m.workPolicy == nil || m.ids == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.service == nil || m.service.audit == nil || m.service.events == nil {
		return authz.DataConstraint{}, ErrUnavailable
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return authz.DataConstraint{}, err
	}
	c, err := m.workPolicy(ctx, tx, tenant, entity, "requirement_write", actor)
	if err != nil {
		return c, err
	}
	if _, err = workScope(c, entity, query.BindRequirements(tx, m.service.names).CreatedBy); err != nil {
		return c, err
	}
	if !c.AllowsEntityRows(entity) {
		return c, ErrWorkForbidden
	}
	h, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return c, err
	}
	if !active || h.ID != actor {
		return c, ErrInvalidMember
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return c, err
	}
	if !active {
		return c, ErrInvalidMember
	}
	p := query.BindProjects(tx, m.service.names)
	project, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(entity), p.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c, ErrProjectNotFound
	}
	if err != nil {
		return c, err
	}
	if !slices.Contains([]string{"INTAKE", "DISCOVERY", "PLANNING", "ACTIVE", "BLOCKED", "ACCEPTANCE_PENDING"}, project.Stage) && !(scopeChange && project.Stage == "DELIVERED") {
		return c, ErrRequirementStage
	}
	return c, nil
}

func (m *Module) createRequirementOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, in requirementCreateBody) (RequirementView, error) {
	var empty RequirementView
	in, err := validateRequirement(in)
	if err != nil {
		return empty, err
	}
	c, err := m.requirementWriterOn(ctx, tx, tenant, entity, actor)
	if err != nil {
		return empty, err
	}
	if !c.AllowAll && !slices.Contains(c.ResourceIDs, entity.String()) && !slices.Contains(c.OwnerIDs, actor.String()) {
		return empty, ErrWorkForbidden
	}
	return m.createAuthorizedRequirementOn(ctx, tx, tenant, entity, actor, in, events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:requirement_write"})
}

// All callers authorize and lock the principal/project before this shared write.
func (m *Module) createAuthorizedRequirementOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, in requirementCreateBody, authority events.Actor) (RequirementView, error) {
	var empty RequirementView
	r := query.BindRequirements(tx, m.service.names)
	count, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ArchivedAt.IsNull()).Count()
	if err != nil {
		return empty, err
	}
	if count >= 500 {
		return empty, ErrRequirementLimit
	}
	number, err := m.nextProjectNumberOn(ctx, tx, tenant, entity, actor, true)
	if err != nil {
		return empty, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.Requirement{TenantID: tenant, ID: guid.ID(id), ProjectID: entity, Number: number, Title: in.Title, Description: in.Description, Priority: in.Priority, Status: "DRAFT", ReviewState: "NEEDS_WORK", Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor}
	if err = r.WithContext(ctx).Create(row); err != nil {
		return empty, err
	}
	cq := query.BindCriteria(tx, m.service.names)
	for i, item := range in.Criteria {
		cid, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		if e = cq.WithContext(ctx).Create(&model.AcceptanceCriterion{TenantID: tenant, ID: guid.ID(cid), ProjectID: entity, RequirementID: row.ID, Code: fmt.Sprintf("C%d", i+1), Text: item.Text, Required: item.Required, Position: i, CreatedAt: now, CreatedBy: &actor}); e != nil {
			return empty, e
		}
	}
	if err = m.requirementEventOn(ctx, tx, row, actor, "delivery.requirement_created", "requirement.created", "", authority); err != nil {
		return empty, err
	}
	return requirementRecord(row), nil
}

func (m *Module) baselineRequirementOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor guid.ID, expected int) (requirementBaselineView, error) {
	var empty requirementBaselineView
	c, err := m.requirementWriterOn(ctx, tx, tenant, entity, actor)
	if err != nil {
		return empty, err
	}
	r := query.BindRequirements(tx, m.service.names)
	scope, err := workScope(c, entity, r.CreatedBy)
	if err != nil {
		return empty, err
	}
	base := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ID.Eq(id), r.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	row, err := base.Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	if expected != row.Version {
		return empty, ErrRequirementVersion
	}
	if row.Status != "DRAFT" || row.CurrentVersionNo != 0 {
		return empty, ErrRequirementFrozen
	}
	cq := query.BindCriteria(tx, m.service.names)
	criteria, err := cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ProjectID.Eq(entity), cq.RequirementID.Eq(id), cq.ArchivedAt.IsNull(), cq.RetiredInVersion.IsNull()).Order(cq.Position, cq.ID).Find()
	if err != nil {
		return empty, err
	}
	snapshot := requirementSnapshot{Title: row.Title, Description: row.Description, Priority: row.Priority, Criteria: make([]requirementCriterionView, 0, len(criteria))}
	required := false
	for _, criterion := range criteria {
		required = required || criterion.Required
		criterion.IntroducedInVersion = 1
		snapshot.Criteria = append(snapshot.Criteria, requirementCriterionRecord(criterion))
	}
	if !required || len(criteria) > 50 || strings.TrimSpace(row.Title) == "" {
		return empty, ErrInvalidArgument
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return empty, err
	}
	hash := sha256.Sum256(encoded)
	criteriaJSON, err := json.Marshal(snapshot.Criteria)
	if err != nil {
		return empty, err
	}
	vid, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	v := &model.RequirementVersion{TenantID: tenant, ID: guid.ID(vid), ProjectID: entity, RequirementID: id, VersionNo: 1, Title: row.Title, Description: row.Description, Priority: row.Priority, Criteria: string(criteriaJSON), ContentHash: hex.EncodeToString(hash[:]), CreatedAt: now, CreatedBy: &actor}
	if err = m.freezeRequirementSuiteOn(ctx, tx, row, v); err != nil {
		return empty, err
	}
	if err = query.BindRequirementVersions(tx, m.service.names).WithContext(ctx).Create(v); err != nil {
		return empty, err
	}
	if _, err = cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ProjectID.Eq(entity), cq.RequirementID.Eq(id), cq.ArchivedAt.IsNull(), cq.RetiredInVersion.IsNull()).UpdateSimple(cq.IntroducedInVersion.Value(1)); err != nil {
		return empty, err
	}
	result, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ID.Eq(id), r.Version.Eq(expected)).UpdateSimple(r.Status.Value("BASELINED"), r.ReviewState.Value("NEEDS_WORK"), r.CurrentVersionNo.Value(1), r.Version.Value(expected+1), r.UpdatedAt.Value(now), r.UpdatedBy.Value(actor))
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrRequirementVersion
	}
	row.Status = "BASELINED"
	row.ReviewState = "NEEDS_WORK"
	row.CurrentVersionNo = 1
	row.Version++
	row.UpdatedAt = now
	if err = m.requirementEventOn(ctx, tx, row, actor, "delivery.requirement_baselined", "requirement.versioned", v.ContentHash); err != nil {
		return empty, err
	}
	return requirementBaselineView{Requirement: requirementRecord(row), Version: requirementBaselineReceipt{VersionNo: 1, ContentHash: v.ContentHash, SuiteHash: v.SuiteHash}}, nil
}

func (m *Module) requirementEventOn(ctx context.Context, tx *gorm.DB, row *model.Requirement, actor guid.ID, auditType, eventType, hash string, authorities ...events.Actor) error {
	if err := m.cancelChangedRequirementSheetsOn(ctx, tx, row); err != nil {
		return err
	}
	authority := events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:requirement_write"}
	if len(authorities) > 0 {
		authority = authorities[0]
	}
	e := audit.NewDomainEvent(ctx, row.TenantID, auditType, "requirement", row.ID)
	e.Detail["actor_kind"], e.Detail["authority_source"] = authority.Kind, authority.AuthoritySource
	e.Detail["project_id"] = row.ProjectID.String()
	e.Detail["version_no"] = row.CurrentVersionNo
	if hash != "" {
		e.Detail["content_hash"] = hash
	}
	if err := m.service.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: eventType, Actor: authority, Subject: events.Subject{Kind: "requirement", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"number": row.Number, "version_no": row.CurrentVersionNo, "content_hash": hash}})
	return err
}

type requirementPatchBody struct {
	Title       *string `json:"title,omitempty" minLength:"1" maxLength:"200"`
	Description *string `json:"description,omitempty" maxLength:"20000"`
	Priority    *int    `json:"priority,omitempty" minimum:"0" maximum:"3"`
}
type criterionCreateBody struct {
	Text     string `json:"text" minLength:"1" maxLength:"2000"`
	Required bool   `json:"required"`
	Position int    `json:"position" minimum:"0" maximum:"49"`
}
type criterionPatchBody struct {
	Text     *string `json:"text,omitempty" minLength:"1" maxLength:"2000"`
	Required *bool   `json:"required,omitempty"`
	Position *int    `json:"position,omitempty" minimum:"0" maximum:"49"`
	Archived *bool   `json:"archived,omitempty"`
}

// A draft edit and its concurrency increment are one atomic command. All
// criterion mutations use the Requirement version, never an unrelated row token.
func (m *Module) editRequirementOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor guid.ID, expected int, change func(*model.Requirement) error) (RequirementView, error) {
	var empty RequirementView
	c, err := m.requirementWriterOn(ctx, tx, tenant, entity, actor)
	if err != nil {
		return empty, err
	}
	r := query.BindRequirements(tx, m.service.names)
	scope, err := workScope(c, entity, r.CreatedBy)
	if err != nil {
		return empty, err
	}
	base := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ID.Eq(id), r.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	row, err := base.Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	if row.Version != expected {
		return empty, ErrRequirementVersion
	}
	if row.Status != "DRAFT" || row.CurrentVersionNo != 0 {
		return empty, ErrRequirementFrozen
	}
	if err = change(row); err != nil {
		return empty, err
	}
	row.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	row.UpdatedBy = &actor
	row.Version++
	row.ReviewState = "NEEDS_WORK"
	if _, err = r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ID.Eq(id)).Updates(&model.Requirement{ReviewState: row.ReviewState}); err != nil {
		return empty, err
	}
	result, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ID.Eq(id), r.Version.Eq(expected)).UpdateSimple(r.Title.Value(row.Title), r.Description.Value(row.Description), r.Priority.Value(row.Priority), r.Version.Value(row.Version), r.UpdatedAt.Value(row.UpdatedAt), r.UpdatedBy.Value(actor))
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrRequirementVersion
	}
	if err = m.requirementEventOn(ctx, tx, row, actor, "delivery.requirement_updated", "requirement.updated", ""); err != nil {
		return empty, err
	}
	return requirementRecord(row), nil
}

func (m *Module) patchRequirementOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor guid.ID, expected int, in requirementPatchBody) (RequirementView, error) {
	return m.editRequirementOn(ctx, tx, tenant, entity, id, actor, expected, func(row *model.Requirement) error {
		if in.Title != nil {
			row.Title = *in.Title
		}
		if in.Description != nil {
			row.Description = *in.Description
		}
		if in.Priority != nil {
			row.Priority = *in.Priority
		}
		validated, err := validateRequirement(requirementCreateBody{Title: row.Title, Description: row.Description, Priority: row.Priority})
		if err != nil {
			return err
		}
		row.Title = validated.Title
		return nil
	})
}

func (m *Module) createCriterionOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor guid.ID, expected int, in criterionCreateBody) (RequirementView, error) {
	return m.editRequirementOn(ctx, tx, tenant, entity, id, actor, expected, func(_ *model.Requirement) error {
		text := strings.TrimSpace(in.Text)
		if text == "" || utf8.RuneCountInString(text) > 2000 || in.Position < 0 || in.Position > 49 {
			return ErrInvalidArgument
		}
		cq := query.BindCriteria(tx, m.service.names)
		rows, err := cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ProjectID.Eq(entity), cq.RequirementID.Eq(id)).Find()
		if err != nil {
			return err
		}
		active, maxCode := 0, 0
		for _, row := range rows {
			if row.ArchivedAt == nil && row.RetiredInVersion == nil {
				active++
			}
			n, e := strconv.Atoi(strings.TrimPrefix(row.Code, "C"))
			if e != nil || !strings.HasPrefix(row.Code, "C") || n < 1 {
				return ErrUnavailable
			}
			maxCode = max(maxCode, n)
		}
		if active >= 50 {
			return ErrCriterionLimit
		}
		code := fmt.Sprintf("C%d", maxCode+1)
		if len(code) > 16 {
			return ErrCriterionLimit
		}
		cid, err := m.ids.Next()
		if err != nil {
			return err
		}
		return cq.WithContext(ctx).Create(&model.AcceptanceCriterion{TenantID: tenant, ID: guid.ID(cid), ProjectID: entity, RequirementID: id, Code: code, Text: text, Required: in.Required, Position: in.Position, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), CreatedBy: &actor})
	})
}

func (m *Module) patchCriterionOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, criterionID, actor guid.ID, expected int, in criterionPatchBody) (RequirementView, error) {
	return m.editRequirementOn(ctx, tx, tenant, entity, id, actor, expected, func(_ *model.Requirement) error {
		cq := query.BindCriteria(tx, m.service.names)
		row, err := cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ProjectID.Eq(entity), cq.RequirementID.Eq(id), cq.ID.Eq(criterionID), cq.RetiredInVersion.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrProjectNotFound
		}
		if err != nil {
			return err
		}
		if in.Text != nil {
			row.Text = strings.TrimSpace(*in.Text)
		}
		if in.Required != nil {
			row.Required = *in.Required
		}
		if in.Position != nil {
			row.Position = *in.Position
		}
		if row.Text == "" || utf8.RuneCountInString(row.Text) > 2000 || row.Position < 0 || row.Position > 49 {
			return ErrInvalidArgument
		}
		if in.Archived != nil {
			if *in.Archived {
				now := time.Now().UTC().Truncate(time.Millisecond)
				row.ArchivedAt = &now
			} else if row.ArchivedAt != nil {
				n, e := cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ProjectID.Eq(entity), cq.RequirementID.Eq(id), cq.ArchivedAt.IsNull(), cq.RetiredInVersion.IsNull()).Count()
				if e != nil {
					return e
				}
				if n >= 50 {
					return ErrCriterionLimit
				}
				row.ArchivedAt = nil
			}
		}
		updates := []field.AssignExpr{cq.Text.Value(row.Text), cq.Required.Value(row.Required), cq.Position.Value(row.Position)}
		if row.ArchivedAt == nil {
			updates = append(updates, cq.ArchivedAt.Null())
		} else {
			updates = append(updates, cq.ArchivedAt.Value(*row.ArchivedAt))
		}
		_, err = cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ProjectID.Eq(entity), cq.RequirementID.Eq(id), cq.ID.Eq(criterionID)).UpdateSimple(updates...)
		return err
	})
}
