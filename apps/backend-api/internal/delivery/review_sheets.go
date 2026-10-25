package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

func (m *Module) validateTriageAgentOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	if m.triageAgents.AgentOn == nil || m.members == nil {
		return ErrUnavailable
	}
	a, found, err := m.triageAgents.AgentOn(ctx, tx, tenant, id)
	if err != nil {
		return err
	}
	if !found || a.ID != id || a.TenantID != tenant || a.State != "ACTIVE" {
		return ErrWorkForbidden
	}
	if err = m.lockSheetParticipantsOn(ctx, tx, []guid.ID{a.PrincipalID}); err != nil {
		return err
	}
	a, found, err = m.triageAgents.AgentOn(ctx, tx, tenant, id)
	if err != nil {
		return err
	}
	if !found || a.ID != id || a.TenantID != tenant || a.State != "ACTIVE" {
		return ErrWorkForbidden
	}
	active, err := m.members.IsMemberActiveOn(ctx, tx, tenant, a.PrincipalID)
	if err != nil {
		return err
	}
	if !active {
		return ErrWorkForbidden
	}
	return nil
}

func (m *Module) registerReviewSheetInbox(s *module.Services) error {
	sources, err := module.Resolve[governance.InboxSources](s)
	if err != nil {
		return err
	}
	return sources.Register(governance.InboxSource{Kind: "REVIEW_SHEET", Read: m.reviewSheetInbox})
}

// reviewSheetInbox derives invitations from open sheets under current IAM.
func (m *Module) reviewSheetInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	out := []governance.InboxItem{}
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return out, ErrWorkForbidden
	}
	if req.Kind != "REVIEW_SHEET" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return out, ErrInvalidArgument
	}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return out, governance.ErrUnavailable
	}
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindReviewSheets(tx, m.service.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.Eq("OPEN")).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if req.ProjectID != 0 && row.ProjectID != req.ProjectID {
				continue
			}
			v, err := sheetView(row)
			if err != nil {
				return err
			}
			if !slices.Contains(v.Document.Reviewers, req.PrincipalID) {
				continue
			}
			if err = m.sheetAuthorityOn(ctx, tx, row.ProjectID, "review_participate"); errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrWorkProjectReadOnly) || errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if _, _, err = m.reviewTargetOn(ctx, tx, row.ProjectID, row.RequirementID, row.WorkItemID); errors.Is(err, ErrWorkForbidden) || errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "NORMAL", CreatedAt: row.CreatedAt, Kind: req.Kind, ID: row.ID}, ProjectID: row.ProjectID, Title: v.Document.Title, ObjectType: "review_sheet", ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
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

type RequirementReviewOpinion struct {
	ActorKind  string            `json:"actor_kind,omitempty" enum:"HUMAN,AGENT"`
	ReviewerID guid.ID           `json:"reviewer_id,omitempty"`
	Decision   string            `json:"decision,omitempty" enum:"PASS,REJECT,SUGGEST"`
	Comment    string            `json:"comment,omitempty" maxLength:"2000"`
	Criteria   map[string]string `json:"criteria"`
	UpdatedAt  time.Time         `json:"updated_at,omitempty"`
}
type RequirementReviewOpinionRecord struct {
	ID           guid.ID                  `json:"id"`
	SupersedesID *guid.ID                 `json:"supersedes_id,omitempty"`
	Opinion      RequirementReviewOpinion `json:"opinion"`
}
type requirementReviewHistoryInput struct {
	RequirementReviewSheetPath
	Offset int `query:"offset" minimum:"0" default:"0"`
	Limit  int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}
type RequirementReviewDocument struct {
	Decision           *RequirementReviewDecision `json:"decision,omitempty"`
	TargetKind         string                     `json:"target_kind,omitempty" enum:"REQUIREMENT,WORK_ITEM"`
	WorkItemID         *guid.ID                   `json:"work_item_id,omitempty"`
	WorkItemVersion    int                        `json:"work_item_version,omitempty"`
	ReviewTypeSnapshot *RequirementReviewTypeView `json:"review_type_snapshot,omitempty"`
	ReviewNode         string                     `json:"review_node,omitempty"`
	Description        string                     `json:"description,omitempty"`
	Title              string                     `json:"title"`
	ReviewType         string                     `json:"review_type"`
	RequirementVersion int                        `json:"requirement_version"`
	BaselineVersion    int                        `json:"baseline_version"`
	Reviewers          []guid.ID                  `json:"reviewers"`
	Minimum            int                        `json:"minimum"`
	DueAt              *time.Time                 `json:"due_at,omitempty"`
	Criteria           []requirementCriterionView `json:"criteria"`
	Opinions           []RequirementReviewOpinion `json:"opinions"`
	ReworkIDs          []guid.ID                  `json:"rework_ids"`
	Reason             string                     `json:"reason,omitempty"`
}
type RequirementReviewSheetView struct {
	WorkItemID    *guid.ID                  `json:"work_item_id,omitempty"`
	ID            guid.ID                   `json:"id"`
	RequirementID guid.ID                   `json:"requirement_id"`
	InitiatorID   guid.ID                   `json:"initiator_id"`
	State         string                    `json:"state"`
	Version       int                       `json:"version"`
	Document      RequirementReviewDocument `json:"document"`
}
type RequirementReviewCreate struct {
	WorkItemID         *guid.ID   `json:"work_item_id,omitempty"`
	WorkItemVersion    int        `json:"work_item_version,omitempty" minimum:"1"`
	ReviewTypeID       *guid.ID   `json:"review_type_id,omitempty"`
	RequirementID      guid.ID    `json:"requirement_id,omitempty"`
	RequirementVersion int        `json:"requirement_version,omitempty" minimum:"1"`
	ReviewType         string     `json:"review_type,omitempty" maxLength:"100"`
	Reviewers          []guid.ID  `json:"reviewers,omitempty" maxItems:"30"`
	Minimum            int        `json:"minimum,omitempty" minimum:"1" maximum:"30"`
	DueAt              *time.Time `json:"due_at,omitempty"`
}
type RequirementReviewSheetPath struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	SheetID  string `path:"sheet_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type sheetList struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}
type sheetCreateInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     RequirementReviewCreate
}
type sheetActionInput struct {
	RequirementReviewSheetPath
	Key     string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch string `header:"If-Match" required:"true" doc:"Quoted review sheet version; must match body.version."`
	Body    struct {
		Version int                      `json:"version" minimum:"1"`
		Action  string                   `json:"action" enum:"OPINION,CLOSE,CANCEL"`
		Opinion RequirementReviewOpinion `json:"opinion,omitempty"`
		Reason  string                   `json:"reason,omitempty" maxLength:"2000"`
	}
}

func sheetView(r *model.ReviewSheet) (RequirementReviewSheetView, error) {
	v := RequirementReviewSheetView{ID: r.ID, WorkItemID: r.WorkItemID, RequirementID: r.RequirementID, InitiatorID: r.InitiatorID, State: r.State, Version: r.Version}
	e := json.Unmarshal([]byte(r.Document), &v.Document)
	return v, e
}
func (m *Module) sheetAuthorityOn(ctx context.Context, tx *gorm.DB, project guid.ID, permission string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	tenant := authn.TenantIDFromContext(ctx)
	if tx == nil || actor <= 0 || tenant <= 0 || project <= 0 {
		return ErrWorkForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.workPolicy == nil || m.members == nil {
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
		if permission != "review_participate" || m.agents.ByPrincipalOn == nil {
			return ErrWorkForbidden
		}
		agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if err != nil {
			return err
		}
		if !found || agent.PrincipalID != actor || agent.TenantID != tenant || agent.State != "ACTIVE" {
			return ErrWorkForbidden
		}
	}
	member, e := m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if e != nil {
		return e
	}
	if !member {
		return ErrWorkForbidden
	}
	c, e := m.workPolicy(ctx, tx, tenant, project, permission, actor)
	if e != nil {
		return e
	}
	if !c.AllowsEntityRows(project) {
		return ErrWorkForbidden
	}
	p := query.BindProjects(tx, m.service.names)
	row, e := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).First()
	if e != nil {
		return e
	}
	if slices.Contains([]string{"CLOSED", "CANCELLED"}, row.Stage) { //nolint:misspell // Frozen persisted protocol state.
		return ErrWorkProjectReadOnly
	}
	return nil
}
func (m *Module) sheetRequirementOn(ctx context.Context, tx *gorm.DB, project, id guid.ID) (*model.Requirement, error) {
	q := query.BindRequirements(tx, m.service.names)
	c, e := m.workPolicy(ctx, tx, authn.TenantIDFromContext(ctx), project, "requirement_read", authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return nil, e
	}
	scope, e := workScope(c, project, q.CreatedBy)
	if e != nil {
		return nil, e
	}
	base := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(id), q.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	return base.Clauses(clause.Locking{Strength: "UPDATE"}).First()
}
func (m *Module) saveSheetOn(ctx context.Context, tx *gorm.DB, r *model.ReviewSheet, d RequirementReviewDocument, kind string) (RequirementReviewSheetView, error) {
	b, e := json.Marshal(d)
	if e != nil {
		return RequirementReviewSheetView{}, e
	}
	r.Document = string(b)
	r.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindReviewSheets(tx, m.service.names)
	if e = q.WithContext(ctx).Save(r); e != nil {
		return RequirementReviewSheetView{}, e
	}
	if e = m.service.audit(ctx, tx, audit.NewDomainEvent(ctx, r.TenantID, "delivery."+kind, "review_sheet", r.ID)); e != nil {
		return RequirementReviewSheetView{}, e
	}
	_, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: r.ProjectID, Type: kind, Actor: events.Actor{Kind: sheetEventActorKind(d, authn.PrincipalIDFromContext(ctx)), ID: authn.ActorIDFromContext(ctx), AuthoritySource: "IAM"}, Subject: events.Subject{Kind: "review_sheet", ID: r.ID}, SubjectVersion: int64(r.Version), Data: map[string]any{"requirement_id": r.RequirementID.String(), "work_item_id": r.WorkItemID, "target_kind": d.TargetKind, "state": r.State, "reviewers": d.Reviewers}})
	if e != nil {
		return RequirementReviewSheetView{}, e
	}
	return sheetView(r)
}
func (m *Module) createSheetOn(ctx context.Context, tx *gorm.DB, project guid.ID, in RequirementReviewCreate) (RequirementReviewSheetView, error) {
	var out RequirementReviewSheetView
	in, reviewType, e := m.prepareReviewCreateOn(ctx, tx, project, in)
	if e != nil {
		return out, e
	}
	if e := m.lockSheetParticipantsOn(ctx, tx, in.Reviewers); e != nil {
		return out, e
	}
	if e := m.sheetAuthorityOn(ctx, tx, project, "review_initiate"); e != nil {
		return out, e
	}
	if reviewType != nil {
		q := query.BindReviewTypes(tx, m.service.names)
		row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(reviewType.ID)).First()
		if e != nil {
			return out, e
		}
		if row.Version != reviewType.SourceVersion {
			return out, ErrWorkVersion
		}
	}
	if (in.RequirementID <= 0 && in.WorkItemID == nil) || (in.RequirementID > 0 && in.WorkItemID != nil) || in.Minimum < 1 || in.Minimum > len(in.Reviewers) || len(in.Reviewers) > 30 || strings.TrimSpace(in.ReviewType) == "" || utf8.RuneCountInString(in.ReviewType) > 100 || (in.DueAt != nil && !in.DueAt.After(time.Now())) {
		return out, ErrInvalidArgument
	}
	req, work, e := m.reviewTargetOn(ctx, tx, project, in.RequirementID, in.WorkItemID)
	if e != nil {
		return out, e
	}
	if (work == nil && req.Version != in.RequirementVersion) || (work != nil && work.Version != in.WorkItemVersion) {
		return out, ErrRequirementVersion
	}
	if work == nil && !slices.Contains([]string{"DRAFT", "BASELINED"}, req.Status) {
		return out, ErrRequirementStage
	}
	if reviewType != nil && ((!slices.Contains(reviewType.Settings.ReviewNodes, req.Status)) || (work != nil && !slices.Contains(reviewType.Settings.AppliesTo.WorkItemTypes, work.Type))) {
		return out, ErrRequirementStage
	}
	reviewers := slices.Clone(in.Reviewers)
	slices.Sort(reviewers)
	humanCount := 0
	for i, id := range reviewers {
		if id <= 0 || (i > 0 && reviewers[i-1] == id) {
			return out, ErrInvalidArgument
		}
		if e = m.principals.Lock(ctx, tx, id); e != nil {
			return out, e
		}
		h, ok, err := m.principals.ActiveHumanByID(ctx, tx, id)
		if err != nil {
			return out, err
		}
		if ok && h.ID == id {
			humanCount++
		} else {
			if m.agents.ByPrincipalOn == nil {
				return out, ErrInvalidMember
			}
			agent, found, err := m.agents.ByPrincipalOn(ctx, tx, req.TenantID, id)
			if err != nil {
				return out, err
			}
			if !found || agent.State != "ACTIVE" || agent.PrincipalID != id || agent.TenantID != req.TenantID {
				return out, ErrInvalidMember
			}
		}
		ok, err = m.members.IsMemberActiveOn(ctx, tx, req.TenantID, id)
		if err != nil {
			return out, err
		}
		if !ok {
			return out, ErrInvalidMember
		}
	}
	if humanCount < in.Minimum {
		return out, ErrInvalidArgument
	}
	q := query.BindReviewSheets(tx, m.service.names)
	open := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.OpenRequirementID.Eq(req.ID))
	if work != nil {
		open = q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.OpenWorkItemID.Eq(work.ID))
	}
	n, e := open.Count()
	if e != nil {
		return out, e
	}
	if n > 0 {
		return out, ErrWorkVersion
	}
	rows, e := m.reviewTargetCriteriaOn(ctx, tx, req, work)
	if e != nil {
		return out, e
	}
	if len(rows) == 0 {
		return out, ErrInvalidArgument
	}
	d := RequirementReviewDocument{Title: req.Title, ReviewType: in.ReviewType, RequirementVersion: req.Version, BaselineVersion: req.CurrentVersionNo, Reviewers: reviewers, Minimum: in.Minimum, DueAt: in.DueAt, Criteria: []requirementCriterionView{}, Opinions: []RequirementReviewOpinion{}, ReworkIDs: []guid.ID{}}
	d.TargetKind = "REQUIREMENT"
	if work != nil {
		d.TargetKind = "WORK_ITEM"
		d.WorkItemID = &work.ID
		d.WorkItemVersion = work.Version
	}
	d.ReviewTypeSnapshot = reviewType
	d.ReviewNode = req.Status
	d.Description = req.Description
	if reviewType != nil && !slices.Contains(reviewType.Settings.ContentFields, "description") {
		d.Description = ""
	}
	d.Criteria = append(d.Criteria, rows...)
	id, e := m.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &model.ReviewSheet{ID: guid.ID(id), TenantID: req.TenantID, ProjectID: project, RequirementID: req.ID, OpenRequirementID: &req.ID, InitiatorID: authn.PrincipalIDFromContext(ctx), State: "OPEN", Version: 1, CreatedAt: now}
	if work != nil {
		r.RequirementID = 0
		r.OpenRequirementID = nil
		r.OpenWorkItemID = nil
		r.WorkItemID = &work.ID
		r.OpenWorkItemID = &work.ID
	}
	if work == nil {
		if e = m.setRequirementReviewStateOn(ctx, tx, req, "REVIEW_PENDING"); e != nil {
			return out, e
		}
		d.RequirementVersion = req.Version
	}
	return m.saveSheetOn(ctx, tx, r, d, "review_sheet.opened")
}
func validateRequirementReviewOpinion(d RequirementReviewDocument, in RequirementReviewOpinion) error {
	if !slices.Contains([]string{"PASS", "REJECT", "SUGGEST"}, in.Decision) || utf8.RuneCountInString(in.Comment) > 2000 || len(in.Criteria) > len(d.Criteria) {
		return ErrInvalidArgument
	}
	valid := map[string]bool{}
	for _, c := range d.Criteria {
		valid[c.ID.String()] = true
	}
	for id, comment := range in.Criteria {
		if !valid[id] || strings.TrimSpace(comment) == "" || utf8.RuneCountInString(comment) > 2000 {
			return ErrInvalidArgument
		}
	}
	if in.Decision == "REJECT" && len(in.Criteria) == 0 && (d.ReviewTypeSnapshot == nil || d.ReviewTypeSnapshot.Settings.PerACRequired) {
		return ErrInvalidArgument
	}
	return nil
}
func (m *Module) actionSheetOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, in *sheetActionInput) (RequirementReviewSheetView, error) {
	var out RequirementReviewSheetView
	permission := "review_initiate"
	if in.Body.Action == "OPINION" {
		permission = "review_participate"
	}
	if e := m.sheetActionAuthorityOn(ctx, tx, project, id, permission); e != nil {
		return out, e
	}
	q := query.BindReviewSheets(tx, m.service.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if e != nil {
		return out, e
	}
	out, e = sheetView(r)
	if e != nil {
		return out, e
	}
	if r.Version != in.Body.Version || r.State != "OPEN" {
		return out, ErrWorkVersion
	}
	req, work, e := m.reviewTargetOn(ctx, tx, project, r.RequirementID, r.WorkItemID)
	if e != nil {
		return out, e
	}
	d := out.Document
	if (work == nil && req.Version != d.RequirementVersion) || (work != nil && work.Version != d.WorkItemVersion) {
		r.State = "CANCELLED" //nolint:misspell // Frozen persisted protocol state.
		r.OpenRequirementID = nil
		r.OpenWorkItemID = nil
		r.Version++
		d.Reason = "TARGET_UPDATED"
		return m.saveSheetOn(ctx, tx, r, d, "review_sheet.closed")
	}
	actor := authn.PrincipalIDFromContext(ctx)
	switch in.Body.Action {
	case "OPINION":
		if !slices.Contains(d.Reviewers, actor) {
			return out, ErrWorkForbidden
		}
		op := in.Body.Opinion
		if op.Criteria == nil {
			op.Criteria = map[string]string{}
		}
		human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
		if err != nil {
			return out, err
		}
		op.ActorKind = "HUMAN"
		if !active || human.ID != actor {
			op.ActorKind = "AGENT"
			if op.Decision != "SUGGEST" {
				return out, ErrWorkForbidden
			}
		}
		if e = validateRequirementReviewOpinion(d, op); e != nil {
			return out, e
		}
		op.ReviewerID = actor
		op.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if e = m.appendReviewOpinionOn(ctx, tx, r, op); e != nil {
			return out, e
		}
		d.Opinions = slices.DeleteFunc(d.Opinions, func(v RequirementReviewOpinion) bool { return v.ReviewerID == actor })
		d.Opinions = append(d.Opinions, op)
	case "CANCEL":
		if r.InitiatorID != actor {
			return out, ErrWorkForbidden
		}
		if strings.TrimSpace(in.Body.Reason) == "" || utf8.RuneCountInString(in.Body.Reason) > 2000 {
			return out, ErrInvalidArgument
		}
		d.Reason = in.Body.Reason
		r.State = "CANCELLED" //nolint:misspell // Frozen persisted protocol state.
		r.OpenRequirementID = nil
		r.OpenWorkItemID = nil
		if work == nil {
			if e = m.setRequirementReviewStateOn(ctx, tx, req, "NEEDS_WORK"); e != nil {
				return out, e
			}
		}
	case "CLOSE":
		if r.InitiatorID != actor {
			return out, ErrWorkForbidden
		}
		valid := []RequirementReviewOpinion{}
		for _, op := range d.Opinions {
			h, active, err := m.principals.ActiveHumanByID(ctx, tx, op.ReviewerID)
			if err != nil {
				return out, err
			}
			member, err := m.members.IsMemberActiveOn(ctx, tx, r.TenantID, op.ReviewerID)
			if err != nil {
				return out, err
			}
			if active && member && h.ID == op.ReviewerID && op.ActorKind != "AGENT" {
				valid = append(valid, op)
			}
		}
		if len(valid) < d.Minimum {
			return out, ErrWorkVersion
		}
		rejected := map[string]string{}
		anyRejected := false
		for _, op := range valid {
			if op.Decision == "REJECT" {
				anyRejected = true
				for cid, comment := range op.Criteria {
					rejected[cid] += comment + "\n"
				}
			}
		}
		r.State = "PASSED"
		if anyRejected {
			r.State = "REJECTED"
			for _, c := range d.Criteria {
				comment, ok := rejected[c.ID.String()]
				if !ok {
					continue
				}
				description := reviewReworkDescription(r.ID, r.RequirementID, c.ID, comment)
				if work != nil {
					description = reviewTargetReworkDescription(r.ID, "work item", work.ID, c.ID, comment)
				}
				body := workCreateBody{Title: c.Code + ": " + truncateSheetText(c.Text, 170), Description: description, Type: "TASK", Objective: c.Text, AccountablePrincipalID: r.InitiatorID, Criteria: []workCriterionInput{{Text: c.Text, VerificationMethod: c.Text}}}
				if work == nil && req.CurrentVersionNo > 0 {
					body.RequirementID = &req.ID
					body.BaselineVersionNo = &req.CurrentVersionNo
				}
				work, err := m.createWorkOn(ctx, tx, r.TenantID, project, actor, body)
				if err != nil {
					return out, err
				}
				d.ReworkIDs = append(d.ReworkIDs, work.ID)
			}
		}
		if work == nil {
			d.Decision, e = m.decideRequirementReviewOn(ctx, tx, req, r, actor, anyRejected)
			if e != nil {
				return out, e
			}
		}
		r.OpenRequirementID = nil
		r.OpenWorkItemID = nil
	default:
		return out, ErrInvalidArgument
	}
	r.Version++
	kind := "review_sheet.opinion_saved"
	if r.State != "OPEN" {
		kind = "review_sheet.closed"
	}
	return m.saveSheetOn(ctx, tx, r, d, kind)
}

// The caller holds the sheet fence, serializing supersession for every reviewer.
func (m *Module) appendReviewOpinionOn(ctx context.Context, tx *gorm.DB, sheet *model.ReviewSheet, op RequirementReviewOpinion) error {
	q := query.BindReviewOpinions(tx, m.service.names)
	var supersedes *guid.ID
	previous, err := q.WithContext(ctx).Where(q.TenantID.Eq(sheet.TenantID), q.ProjectID.Eq(sheet.ProjectID), q.SheetID.Eq(sheet.ID), q.ReviewerID.Eq(op.ReviewerID)).Order(q.ID.Desc()).First()
	if err == nil {
		supersedes = &previous.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	return q.WithContext(ctx).Create(&model.ReviewOpinion{ID: guid.ID(id), TenantID: sheet.TenantID, ProjectID: sheet.ProjectID, SheetID: sheet.ID, ReviewerID: op.ReviewerID, SupersedesID: supersedes, Document: string(data), CreatedAt: op.UpdatedAt})
}

func (m *Module) listReviewOpinionsHTTP(ctx context.Context, in *requirementReviewHistoryInput) (*respx.Body[[]RequirementReviewOpinionRecord], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	id, err := guid.RequireID(in.SheetID)
	if err != nil || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	out := []RequirementReviewOpinionRecord{}
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindReviewSheets(tx, m.service.names)
		sheet, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if e != nil {
			return e
		}
		if _, _, e = m.reviewTargetOn(ctx, tx, project, sheet.RequirementID, sheet.WorkItemID); e != nil {
			return e
		}
		h := query.BindReviewOpinions(tx, m.service.names)
		base := h.WithContext(ctx).Where(h.TenantID.Eq(sheet.TenantID), h.ProjectID.Eq(project), h.SheetID.Eq(id))
		if total, e = base.Count(); e != nil {
			return e
		}
		rows, e := base.Order(h.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, row := range rows {
			v := RequirementReviewOpinionRecord{ID: row.ID, SupersedesID: row.SupersedesID}
			if e = json.Unmarshal([]byte(row.Document), &v.Opinion); e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return respx.List(ctx, out, respx.Page{Total: total, Offset: in.Offset, Limit: in.Limit}), nil
}

// Preserve stable provenance and explicitly mark excerpts when combined feedback
// exceeds the canonical work description limit. Full submissions remain in history.
func reviewReworkDescription(sheet, requirement, criterion guid.ID, feedback string) string {
	return reviewTargetReworkDescription(sheet, "requirement", requirement, criterion, feedback)
}
func reviewTargetReworkDescription(sheet guid.ID, kind string, target, criterion guid.ID, feedback string) string {
	const limit = 20000
	header := fmt.Sprintf("Review sheet %s; %s %s; criterion %s.\n\n", sheet, kind, target, criterion)
	if utf8.RuneCountInString(header)+utf8.RuneCountInString(feedback) <= limit {
		return header + feedback
	}
	const suffix = "\n\n[Feedback excerpt; full opinions retained in review sheet history.]"
	return header + truncateSheetText(feedback, limit-utf8.RuneCountInString(header)-utf8.RuneCountInString(suffix)) + suffix
}

func truncateSheetText(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func (m *Module) registerSheetsHTTP(api huma.API) {
	m.registerReviewHTTP(api)
	m.registerReviewTypesHTTP(api)
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"entities"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	base := "/entities/{entity_id}/review-sheets"
	authz.RegisterOwned(m.registrar, api, op("review_sheet.list", "GET", base, "List requirement review sheets", 200), authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "read"}, m.archiveReviewsListSheetsHTTP)
	authz.RegisterOwned(m.registrar, api, op("review_sheet.opinions", "GET", base+"/{sheet_id}/opinions", "List immutable review opinion history", 200), authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "read"}, m.listOpinionsHTTP)
	authz.RegisterOwned(m.registrar, api, op("review_sheet.create", "POST", base, "Open a versioned requirement review", 201), authz.EntityPath, owner, authz.Guard{Resource: "review", Verb: "initiate"}, m.createSheetHTTP)
	authz.RegisterOwned(m.registrar, api, op("review_sheet.action", "POST", base+"/{sheet_id}/actions", "Submit latest review opinion or close or cancel a sheet", 200), authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "read"}, m.actionSheetHTTP)
}
func (m *Module) listSheetsHTTP(ctx context.Context, in *sheetList) (*respx.Body[[]RequirementReviewSheetView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	out := []RequirementReviewSheetView{}
	var total int64
	e = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindReviewSheets(tx, m.service.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project)).Order(q.ID.Desc()).Find()
		if err != nil {
			return err
		}
		for _, r := range rows {
			_, _, err = m.reviewTargetOn(ctx, tx, project, r.RequirementID, r.WorkItemID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			total++
			if total <= int64(in.Offset) || len(out) >= in.Limit {
				continue
			}
			v, err := sheetView(r)
			if err != nil {
				return err
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

type RequirementReviewReceipt struct {
	ID      guid.ID `json:"id"`
	State   string  `json:"state"`
	Version int     `json:"version"`
}

func reviewReceipt(v RequirementReviewSheetView) RequirementReviewReceipt {
	return RequirementReviewReceipt{ID: v.ID, State: v.State, Version: v.Version}
}
func (m *Module) createSheetHTTP(ctx context.Context, in *sheetCreateInput) (*deliveryCommandOutput[RequirementReviewReceipt], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	return deliveryCommand(ctx, m, "review_sheet", requirementHTTPError, "review_sheet.create", "POST", "/entities/{entity_id}/review-sheets", in.Key, 201, in.Body, map[string]string{"entity_id": in.EntityID}, func(tx *gorm.DB) (RequirementReviewReceipt, guid.ID, error) {
		v, e := m.createSheetOn(ctx, tx, project, in.Body)
		return reviewReceipt(v), v.ID, e
	}, func(tx *gorm.DB) error {
		prepared, _, e := m.prepareReviewCreateOn(ctx, tx, project, in.Body)
		if e != nil {
			return e
		}
		if e := m.lockSheetParticipantsOn(ctx, tx, prepared.Reviewers); e != nil {
			return e
		}
		return m.sheetAuthorityOn(ctx, tx, project, "review_initiate")
	})
}
func (m *Module) actionSheetHTTP(ctx context.Context, in *sheetActionInput) (*deliveryCommandOutput[RequirementReviewReceipt], error) {
	if in.IfMatch != "" && in.IfMatch != fmtETag(in.Body.Version) {
		return nil, requirementHTTPError(ctx, ErrRequirementVersion)
	}
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.SheetID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	permission := "review_initiate"
	if in.Body.Action == "OPINION" {
		permission = "review_participate"
	}
	return deliveryCommand(ctx, m, "review_sheet", requirementHTTPError, "review_sheet.action", "POST", "/entities/{entity_id}/review-sheets/{sheet_id}/actions", in.Key, 200, in.Body, map[string]string{"entity_id": in.EntityID, "sheet_id": in.SheetID}, func(tx *gorm.DB) (RequirementReviewReceipt, guid.ID, error) {
		v, e := m.actionSheetOn(ctx, tx, project, id, in)
		return reviewReceipt(v), v.ID, e
	}, func(tx *gorm.DB) error { return m.sheetActionAuthorityOn(ctx, tx, project, id, permission) })
}

// cancelChangedRequirementSheetsOn invalidates stale reviews atomically with the
// owning requirement mutation, including draft edits and baseline changes.
func (m *Module) cancelChangedRequirementSheetsOn(ctx context.Context, tx *gorm.DB, req *model.Requirement) error {
	q := query.BindReviewSheets(tx, m.service.names)
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(req.TenantID), q.ProjectID.Eq(req.ProjectID), q.OpenRequirementID.Eq(req.ID)).Find()
	if e != nil {
		return e
	}
	for _, r := range rows {
		v, e := sheetView(r)
		if e != nil {
			return e
		}
		if v.Document.RequirementVersion == req.Version {
			continue
		}
		r.State = "CANCELLED" //nolint:misspell // Frozen persisted protocol state.
		r.OpenRequirementID = nil
		r.OpenWorkItemID = nil
		r.Version++
		v.Document.Reason = "REQUIREMENT_UPDATED"
		if _, e = m.saveSheetOn(ctx, tx, r, v.Document, "review_sheet.closed"); e != nil {
			return e
		}
	}
	return nil
}

func sheetEventActorKind(d RequirementReviewDocument, actor guid.ID) string {
	for _, op := range d.Opinions {
		if op.ReviewerID == actor && op.ActorKind == "AGENT" {
			return "AGENT"
		}
	}
	return "HUMAN"
}

// Every participant fence is acquired in ID order before the project fence,
// matching identity mutation ordering and avoiding reviewer/project lock cycles.
func (m *Module) lockSheetParticipantsOn(ctx context.Context, tx *gorm.DB, participants []guid.ID) error {
	if m.principals.Lock == nil {
		return ErrUnavailable
	}
	if len(participants) > 30 {
		return ErrInvalidArgument
	}
	ids := append(slices.Clone(participants), authn.PrincipalIDFromContext(ctx))
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if id <= 0 {
			return ErrInvalidArgument
		}
		if e := m.principals.Lock(ctx, tx, id); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) sheetActionAuthorityOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, permission string) error {
	q := query.BindReviewSheets(tx, m.service.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if e != nil {
		return e
	}
	v, e := sheetView(r)
	if e != nil {
		return e
	}
	if e = m.lockSheetParticipantsOn(ctx, tx, v.Document.Reviewers); e != nil {
		return e
	}
	if e = m.sheetAuthorityOn(ctx, tx, project, permission); e != nil {
		return e
	}
	if permission == "review_initiate" && v.Document.Decision != nil {
		req, err := m.sheetRequirementOn(ctx, tx, project, r.RequirementID)
		if err != nil {
			return err
		}
		if err = m.requirementReviewWriterOn(ctx, tx, req, authn.PrincipalIDFromContext(ctx)); err != nil {
			return err
		}
		_, e = m.requirementDecisionAuthorityOn(ctx, tx, r.TenantID, project, r.RequirementID, authn.PrincipalIDFromContext(ctx), v.Document.Decision.Action)
		return e
	}
	return nil
}
