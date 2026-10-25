package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type RequirementReviewAppliesTo struct {
	Requirement   bool     `json:"requirement"`
	WorkItemTypes []string `json:"work_item_types" maxItems:"4"`
}
type RequirementReviewTypeSettings struct {
	Name               string                     `json:"name" minLength:"1" maxLength:"60"`
	AppliesTo          RequirementReviewAppliesTo `json:"applies_to"`
	ReviewNodes        []string                   `json:"review_nodes" maxItems:"20"`
	DefaultReviewerIDs []guid.ID                  `json:"default_reviewer_ids" maxItems:"30"`
	MinReviewers       int                        `json:"min_reviewers" minimum:"1" maximum:"10"`
	ContentFields      []string                   `json:"content_fields" maxItems:"3"`
	PerACRequired      bool                       `json:"per_ac_required"`
	Propagate          bool                       `json:"propagate" doc:"When false, existing projects retain their configuration projectCopy; open sheets always retain their snapshot."`
}
type reviewTypeDocument struct {
	RequirementReviewTypeSettings
	ProjectCopies map[string]RequirementReviewTypeView `json:"project_copies,omitempty"`
}

type RequirementReviewTypeView struct {
	SourceVersion int                           `json:"-"`
	ID            guid.ID                       `json:"id"`
	Version       int                           `json:"version"`
	Settings      RequirementReviewTypeSettings `json:"settings"`
}
type RequirementReviewTypeTenantPath struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type RequirementReviewTypePath struct {
	RequirementReviewTypeTenantPath
	TypeID string `path:"type_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type reviewTypeListInput struct {
	RequirementReviewTypeTenantPath
	Offset    int    `query:"offset" minimum:"0" default:"0"`
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit     int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}
type reviewTypeCreateInput struct {
	RequirementReviewTypeTenantPath
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body RequirementReviewTypeSettings
}
type reviewTypeUpdateInput struct {
	RequirementReviewTypePath
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key       string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body      struct {
		Version  int                           `json:"version" minimum:"1"`
		Settings RequirementReviewTypeSettings `json:"settings"`
	}
}

func validateReviewType(in RequirementReviewTypeSettings) error {
	if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 60 || in.MinReviewers < 1 || in.MinReviewers > 10 || len(in.DefaultReviewerIDs) > 30 || len(in.ReviewNodes) > 20 || len(in.ContentFields) > 3 || (!in.AppliesTo.Requirement && len(in.AppliesTo.WorkItemTypes) == 0) {
		return ErrInvalidArgument
	}
	for _, node := range in.ReviewNodes {
		if !slices.Contains([]string{"DRAFT", "BASELINED", "READY", "CLAIMED", "IN_PROGRESS", "PLAN_REVIEW", "AGENT_CLAIMED_DONE", "VERIFYING", "INTERNAL_QA", "NEEDS_HUMAN_REVIEW", "ACCEPTED", "REWORK", "BLOCKED", "EXECUTION_FAILED", "STOPPED", "EXCEPTION", "SKIPPED", "CANCELLED"}, node) { //nolint:misspell // Frozen persisted protocol state.
			return ErrInvalidArgument
		}
	}
	for _, field := range in.ContentFields {
		if !slices.Contains([]string{"title", "description", "criteria"}, field) {
			return ErrInvalidArgument
		}
	}
	for _, kind := range in.AppliesTo.WorkItemTypes {
		if !slices.Contains([]string{"TASK", "BUG", "RISK", "INCIDENT"}, kind) {
			return ErrInvalidArgument
		}
	}
	ids := slices.Clone(in.DefaultReviewerIDs)
	slices.Sort(ids)
	for i, id := range ids {
		if id <= 0 || (i > 0 && ids[i-1] == id) {
			return ErrInvalidArgument
		}
	}
	return nil
}
func reviewTypeView(row *model.ReviewType) (RequirementReviewTypeView, error) {
	v := RequirementReviewTypeView{ID: row.ID, Version: row.Version, SourceVersion: row.Version}
	err := json.Unmarshal([]byte(row.Document), &v.Settings)
	return v, err
}
func (m *Module) reviewTypeAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tenant <= 0 || tenant != authn.TenantIDFromContext(ctx) || actor <= 0 {
		return ErrWorkForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m.reviewTypePolicy == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil {
		return ErrUnavailable
	}
	if e := m.principals.Lock(ctx, tx, actor); e != nil {
		return e
	}
	h, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !active || h.ID != actor {
		return ErrWorkForbidden
	}
	active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if e != nil {
		return e
	}
	if !active {
		return ErrWorkForbidden
	}
	c, e := m.reviewTypePolicy(ctx, tx, tenant, permission, actor)
	if e != nil {
		return e
	}
	if !c.AllowAll {
		return ErrWorkForbidden
	}
	return nil
}
func (m *Module) validateReviewTypeReviewersOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in RequirementReviewTypeSettings) error {
	// Defaults identify real, active tenant members; Agent defaults remain advisory.
	for _, reviewer := range in.DefaultReviewerIDs {
		h, human, e := m.principals.ActiveHumanByID(ctx, tx, reviewer)
		if e != nil {
			return e
		}
		if !human || h.ID != reviewer {
			if m.agents.ByPrincipalOn == nil {
				return ErrUnavailable
			}
			a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, reviewer)
			if e != nil {
				return e
			}
			if !found || a.TenantID != tenant || a.State != "ACTIVE" {
				return ErrInvalidMember
			}
		}
		active, e := m.members.IsMemberActiveOn(ctx, tx, tenant, reviewer)
		if e != nil {
			return e
		}
		if !active {
			return ErrInvalidMember
		}
	}
	return nil
}
func (m *Module) saveReviewTypeOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, in RequirementReviewTypeSettings) (RequirementReviewTypeView, error) {
	var out RequirementReviewTypeView
	if e := validateReviewType(in); e != nil {
		return out, e
	}
	if e := m.lockSheetParticipantsOn(ctx, tx, in.DefaultReviewerIDs); e != nil {
		return out, e
	}
	if e := m.reviewTypeAuthorityOn(ctx, tx, tenant, "workflow_manage"); e != nil {
		return out, e
	}
	if e := m.validateReviewTypeReviewersOn(ctx, tx, tenant, in); e != nil {
		return out, e
	}
	q := query.BindReviewTypes(tx, m.service.names)
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.ReviewType{TenantID: tenant, CreatedAt: now}
	if id != 0 {
		var e error
		row, e = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if e != nil {
			return out, e
		}
		if row.Version != version {
			return out, ErrWorkVersion
		}
	} else {
		if version != 0 {
			return out, ErrWorkVersion
		}
		next, e := m.ids.Next()
		if e != nil {
			return out, e
		}
		row.ID = guid.ID(next)
	}
	document := reviewTypeDocument{RequirementReviewTypeSettings: in}
	if !in.Propagate {
		previous := RequirementReviewTypeView{ID: row.ID, Version: row.Version + 1, Settings: in}
		if id != 0 {
			if e := json.Unmarshal([]byte(row.Document), &document); e != nil {
				return out, e
			}
			previous, _ = reviewTypeView(row)
		}
		if document.ProjectCopies == nil {
			document.ProjectCopies = map[string]RequirementReviewTypeView{}
		}
		projects := query.BindProjects(tx, m.service.names)
		rows, e := projects.WithContext(ctx).Where(projects.TenantID.Eq(tenant), projects.ArchivedAt.IsNull()).Find()
		if e != nil {
			return out, e
		}
		for _, project := range rows {
			if _, exists := document.ProjectCopies[project.ProjectID.String()]; !exists {
				document.ProjectCopies[project.ProjectID.String()] = previous
			}
		}
		document.RequirementReviewTypeSettings = in
	}
	data, e := json.Marshal(document)
	if e != nil {
		return out, e
	}
	row.Document = string(data)
	row.Version++
	row.UpdatedAt = now
	if e = q.WithContext(ctx).Save(row); e != nil {
		return out, e
	}
	if e = m.service.audit(ctx, tx, audit.NewDomainEvent(ctx, tenant, "review_type.configured", "review_type", row.ID)); e != nil {
		return out, e
	}
	_, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "review_type.configured", Actor: events.Actor{Kind: "HUMAN", ID: authn.ActorIDFromContext(ctx), AuthoritySource: "permission:workflow_manage"}, Subject: events.Subject{Kind: "review_type", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"propagate": in.Propagate}})
	if e != nil {
		return out, e
	}
	return reviewTypeView(row)
}

// Project overrides share the team type's row fence, so propagation and local
// changes cannot overwrite each other. Their version is local to the projectCopy.
func (m *Module) saveProjectReviewTypeOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected int, settings RequirementReviewTypeSettings) (RequirementReviewTypeView, error) {
	var out RequirementReviewTypeView
	if e := validateReviewType(settings); e != nil {
		return out, e
	}
	if e := m.lockSheetParticipantsOn(ctx, tx, settings.DefaultReviewerIDs); e != nil {
		return out, e
	}
	if e := m.reviewTypeAuthorityOn(ctx, tx, tenant, "workflow_manage"); e != nil {
		return out, e
	}
	if e := m.sheetAuthorityOn(ctx, tx, project, "workflow_manage"); e != nil {
		return out, e
	}
	q := query.BindReviewTypes(tx, m.service.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if e != nil {
		return out, e
	}
	var document reviewTypeDocument
	if e = json.Unmarshal([]byte(row.Document), &document); e != nil {
		return out, e
	}
	current, e := reviewTypeView(row)
	if e != nil {
		return out, e
	}
	if projectCopy, exists := document.ProjectCopies[project.String()]; exists {
		current = projectCopy
	}
	if current.Version != expected {
		return out, ErrWorkVersion
	}
	if e = m.validateReviewTypeReviewersOn(ctx, tx, tenant, settings); e != nil {
		return out, e
	}
	out = RequirementReviewTypeView{ID: id, Version: current.Version + 1, Settings: settings}
	if document.ProjectCopies == nil {
		document.ProjectCopies = map[string]RequirementReviewTypeView{}
	}
	document.ProjectCopies[project.String()] = out
	data, e := json.Marshal(document)
	if e != nil {
		return out, e
	}
	row.Document = string(data)
	row.Version++
	row.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if e = q.WithContext(ctx).Save(row); e != nil {
		return out, e
	}
	event := audit.NewDomainEvent(ctx, tenant, "review_type.configured", "review_type", id)
	event.Detail["project_id"] = project.String()
	if e = m.service.audit(ctx, tx, event); e != nil {
		return out, e
	}
	_, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "review_type.configured", Actor: events.Actor{Kind: "HUMAN", ID: authn.ActorIDFromContext(ctx), AuthoritySource: "permission:workflow_manage"}, Subject: events.Subject{Kind: "review_type", ID: id}, SubjectVersion: int64(row.Version), Data: map[string]any{"project_id": project.String(), "version": out.Version}})
	return out, e
}

func (m *Module) prepareReviewCreateOn(ctx context.Context, tx *gorm.DB, project guid.ID, in RequirementReviewCreate) (RequirementReviewCreate, *RequirementReviewTypeView, error) {
	if in.ReviewTypeID == nil {
		return in, nil, nil
	}
	if *in.ReviewTypeID <= 0 {
		return in, nil, ErrInvalidArgument
	}
	q := query.BindReviewTypes(tx, m.service.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(*in.ReviewTypeID)).First()
	if e != nil {
		return in, nil, e
	}
	v, e := reviewTypeView(row)
	if e != nil {
		return in, nil, e
	}
	var document reviewTypeDocument
	if e = json.Unmarshal([]byte(row.Document), &document); e != nil {
		return in, nil, e
	}
	if projectCopy, exists := document.ProjectCopies[project.String()]; exists {
		v = projectCopy
		v.SourceVersion = row.Version
	}
	if in.WorkItemID == nil && !v.Settings.AppliesTo.Requirement {
		return in, nil, ErrInvalidArgument
	}
	in.ReviewType = v.Settings.Name
	in.Minimum = v.Settings.MinReviewers
	if len(in.Reviewers) == 0 {
		in.Reviewers = slices.Clone(v.Settings.DefaultReviewerIDs)
	}
	return in, &v, nil
}
func (m *Module) registerReviewTypesHTTP(api huma.API) {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"review-types"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("review_type.list", "GET", "/review-types", "List team review types", 200), authz.Guard{Resource: "requirement", Verb: "read"}, m.archiveReviewsListReviewTypesHTTP)
	authz.Register(m.registrar, api, op("review_type.create", "POST", "/review-types", "Create team review type", 201), authz.Guard{Resource: "workflow", Verb: "manage"}, m.createReviewTypeHTTP)
	authz.Register(m.registrar, api, op("review_type.replace", "PUT", "/review-types/{type_id}", "Replace team review type", 200), authz.Guard{Resource: "workflow", Verb: "manage"}, m.updateReviewTypeHTTP)
}
func (m *Module) listReviewTypesHTTP(ctx context.Context, in *reviewTypeListInput) (*respx.Body[[]RequirementReviewTypeView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil || tenant != authn.TenantIDFromContext(ctx) || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	out := []RequirementReviewTypeView{}
	var total int64
	e = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A tenant-wide read permission is needed for a team-wide configuration list.
		if e := m.reviewTypeAuthorityOn(ctx, tx, tenant, "requirement_read"); e != nil {
			return e
		}
		q := query.BindReviewTypes(tx, m.service.names)
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
		var e error
		total, e = base.Count()
		if e != nil {
			return e
		}
		rows, e := base.Order(q.ID).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, row := range rows {
			v, e := reviewTypeView(row)
			if in.ProjectID != "" {
				project, err := guid.RequireID(in.ProjectID)
				if err != nil {
					return ErrInvalidArgument
				}
				if err = m.sheetAuthorityOn(ctx, tx, project, "requirement_read"); err != nil {
					return err
				}
				var d reviewTypeDocument
				if err = json.Unmarshal([]byte(row.Document), &d); err != nil {
					return err
				}
				if projectCopy, ok := d.ProjectCopies[project.String()]; ok {
					v = projectCopy
				}
			}
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	if e != nil {
		return nil, requirementHTTPError(ctx, e)
	}
	return respx.List(ctx, out, respx.Page{Total: total, Offset: in.Offset, Limit: in.Limit}), nil
}
func (m *Module) createReviewTypeHTTP(ctx context.Context, in *reviewTypeCreateInput) (*deliveryCommandOutput[RequirementReviewTypeView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	return deliveryCommand(ctx, m, "review_type", requirementHTTPError, "review_type.create", "POST", "/tenants/{tenant_id}/review-types", in.Key, 201, in.Body, map[string]string{"tenant_id": in.TenantID}, func(tx *gorm.DB) (RequirementReviewTypeView, guid.ID, error) {
		v, e := m.saveReviewTypeOn(ctx, tx, tenant, 0, 0, in.Body)
		return v, v.ID, e
	}, func(tx *gorm.DB) error {
		if e := m.lockSheetParticipantsOn(ctx, tx, in.Body.DefaultReviewerIDs); e != nil {
			return e
		}
		return m.reviewTypeAuthorityOn(ctx, tx, tenant, "workflow_manage")
	})
}
func (m *Module) updateReviewTypeHTTP(ctx context.Context, in *reviewTypeUpdateInput) (*deliveryCommandOutput[RequirementReviewTypeView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.TypeID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	return deliveryCommand(ctx, m, "review_type", requirementHTTPError, "review_type.replace", "PUT", "/tenants/{tenant_id}/review-types/{type_id}", in.Key, 200, in.Body, map[string]string{"tenant_id": in.TenantID, "type_id": in.TypeID, "project_id": in.ProjectID}, func(tx *gorm.DB) (RequirementReviewTypeView, guid.ID, error) {
		var v RequirementReviewTypeView
		var e error
		if in.ProjectID == "" {
			v, e = m.saveReviewTypeOn(ctx, tx, tenant, id, in.Body.Version, in.Body.Settings)
		} else {
			project, err := guid.RequireID(in.ProjectID)
			if err != nil {
				return v, 0, ErrInvalidArgument
			}
			v, e = m.saveProjectReviewTypeOn(ctx, tx, tenant, project, id, in.Body.Version, in.Body.Settings)
		}
		return v, v.ID, e
	}, func(tx *gorm.DB) error {
		if e := m.lockSheetParticipantsOn(ctx, tx, in.Body.Settings.DefaultReviewerIDs); e != nil {
			return e
		}
		if e := m.reviewTypeAuthorityOn(ctx, tx, tenant, "workflow_manage"); e != nil {
			return e
		}
		if in.ProjectID != "" {
			project, e := guid.RequireID(in.ProjectID)
			if e != nil {
				return ErrInvalidArgument
			}
			return m.sheetAuthorityOn(ctx, tx, project, "workflow_manage")
		}
		return nil
	})
}
