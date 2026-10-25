package templates

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// StudioContent is descriptive source material. QC notes are advisory human
// reports; they cannot authorize a workflow gate or assert verifier results.
type StudioContent struct {
	DraftType     string        `json:"draft_type,omitempty" enum:"ACTION,TRIAGE,STATUS"`
	ReportedState string        `json:"reported_state,omitempty" maxLength:"32"`
	Video         *VideoData    `json:"video,omitempty"`
	Novel         *NovelData    `json:"novel,omitempty"`
	Team          *TeamTaskData `json:"team,omitempty"`
	Title         string        `json:"title" minLength:"1" maxLength:"200"`
	Text          string        `json:"text" maxLength:"20000"`
	Source        string        `json:"source" maxLength:"2000"`
	OwnerID       *guid.ID      `json:"owner_id,omitempty"`
	DueAt         *time.Time    `json:"due_at,omitempty"`
	DurationMS    int           `json:"duration_ms" minimum:"0" maximum:"86400000"`
	Provider      string        `json:"provider" maxLength:"200"`
	Model         string        `json:"model" maxLength:"200"`
	Seed          string        `json:"seed" maxLength:"200"`
}
type StudioRecordView struct {
	model.StudioRecord
	Content StudioContent `json:"content"`
}
type StudioRecordPage struct {
	Items      []StudioRecordView `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
	HasMore    bool               `json:"has_more"`
}
type StudioDraft struct {
	Domain   string        `json:"domain" enum:"VIDEO,NOVEL,TEAM_TASKS"`
	Kind     string        `json:"kind" enum:"SHOT,CANDIDATE,QC_NOTE,CHAPTER,CONTINUITY,FORESHADOW,ASSISTANT_DRAFT"`
	ParentID *guid.ID      `json:"parent_id,omitempty"`
	Content  StudioContent `json:"content"`
}

func studioView(row *model.StudioRecord) (StudioRecordView, error) {
	out := StudioRecordView{StudioRecord: *row}
	err := json.Unmarshal([]byte(row.Payload), &out.Content)
	return out, err
}
func studioDomain(kind string) string {
	switch kind {
	case "SHOT", "CANDIDATE", "QC_NOTE":
		return "VIDEO"
	case "CHAPTER", "CONTINUITY", "FORESHADOW", "STORY_BIBLE":
		return "NOVEL"
	case "ASSISTANT_DRAFT", "TASK":
		return "TEAM_TASKS"
	}
	return ""
}
func (in StudioDraft) validate() error {
	c := in.Content
	if c.DraftType != "" && !slices.Contains([]string{"ACTION", "TRIAGE", "STATUS"}, c.DraftType) {
		return ErrInvalid
	}
	if len(c.ReportedState) > 32 || (c.DraftType == "STATUS" && strings.TrimSpace(c.ReportedState) == "") {
		return ErrInvalid
	}
	// Structured team facts are written only through the fenced team commands.
	if c.Team != nil || c.Novel != nil || c.Video != nil || in.Kind == "STORY_BIBLE" {
		return ErrInvalid
	}
	if studioDomain(in.Kind) != in.Domain || in.Domain == "" || in.Kind == "TASK" || strings.TrimSpace(c.Title) == "" || len(c.Title) > 200 || len(c.Text) > 20000 || len(c.Source) > 2000 || len(c.Provider) > 200 || len(c.Model) > 200 || len(c.Seed) > 200 || c.DurationMS < 0 || c.DurationMS > 86400000 || (c.OwnerID != nil && *c.OwnerID <= 0) || (in.ParentID != nil && *in.ParentID <= 0) {
		return ErrInvalid
	}
	if (in.Kind == "CANDIDATE" || in.Kind == "QC_NOTE" || in.Kind == "CONTINUITY") && in.ParentID == nil {
		return ErrInvalid
	}
	if in.Kind == "CANDIDATE" && strings.TrimSpace(c.Source) == "" {
		return ErrInvalid
	}
	if in.Kind == "ASSISTANT_DRAFT" && strings.TrimSpace(c.Source) == "" {
		return ErrInvalid
	}
	if (in.Kind == "QC_NOTE" || in.Kind == "CONTINUITY" || in.Kind == "FORESHADOW" || in.Kind == "ASSISTANT_DRAFT") && strings.TrimSpace(c.Text) == "" {
		return ErrInvalid
	}
	return nil
}
func (m *Module) studioRecordOn(ctx context.Context, tx *gorm.DB, binding, id guid.ID) (*model.StudioRecord, error) {
	q := query.BindStudioRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.BindingID.Eq(binding), q.ID.Eq(id)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, err
}
func (m *Module) studioAuthorizeOn(ctx context.Context, tx *gorm.DB, project guid.ID) (*model.Binding, error) {
	binding, _, _, err := m.authorizeInstanceCommandOn(ctx, tx, project)
	if err != nil {
		return nil, err
	}
	// Only current active Humans can create or adopt assistant suggestions.
	claims, ok := authn.FromContext(ctx)
	if !ok || claims.SubjectType != authn.SubjectTypePrincipal || m.principals.ActiveHumanByID == nil {
		return nil, ErrForbidden
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if !active || human.ID != authn.PrincipalIDFromContext(ctx) {
		return nil, ErrForbidden
	}
	return binding, nil
}
func (m *Module) createStudioOn(ctx context.Context, tx *gorm.DB, project guid.ID, in StudioDraft) (StudioRecordView, error) {
	var empty StudioRecordView
	if err := in.validate(); err != nil {
		return empty, err
	}
	binding, err := m.studioAuthorizeOn(ctx, tx, project)
	if err != nil {
		return empty, err
	}
	versions := query.BindVersions(tx, m.names)
	version, err := versions.WithContext(ctx).Where(versions.ID.Eq(binding.TemplateVersionID), versions.TenantID.Eq(binding.TenantID)).Take()
	if err != nil {
		return empty, err
	}
	definition, err := versionView(version)
	if err != nil {
		return empty, err
	}
	if definition.Definition.Domain != in.Domain {
		return empty, ErrConflict
	}
	if in.ParentID != nil {
		parent, e := m.studioRecordOn(ctx, tx, binding.ID, *in.ParentID)
		if e != nil {
			return empty, e
		}
		allowed := map[string][]string{"CANDIDATE": {"SHOT"}, "QC_NOTE": {"CANDIDATE", "SHOT"}, "CONTINUITY": {"CHAPTER"}, "FORESHADOW": {"CHAPTER"}}
		if parent.Domain != in.Domain || !slices.Contains(allowed[in.Kind], parent.Kind) {
			return empty, ErrInvalid
		}
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	raw, err := json.Marshal(in.Content)
	if err != nil {
		return empty, err
	}
	row := &model.StudioRecord{ID: guid.ID(id), TenantID: binding.TenantID, BindingID: binding.ID, Domain: in.Domain, Kind: in.Kind, ParentID: in.ParentID, State: "OPEN", Payload: string(raw), Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC()}
	if err = query.BindStudioRecords(tx, m.names).WithContext(ctx).Create(row); err != nil {
		return empty, err
	}
	if err = m.studioEventOn(ctx, tx, project, row, "created"); err != nil {
		return empty, err
	}
	return studioView(row)
}

type StudioDecision struct {
	Version int    `json:"version" minimum:"1"`
	Action  string `json:"action" enum:"SELECT,CONFIRM,REJECT,RESOLVE"`
	Reason  string `json:"reason" minLength:"1" maxLength:"2000"`
}

func (m *Module) decideStudioOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, in StudioDecision) (StudioRecordView, error) {
	var empty StudioRecordView
	if strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 2000 || in.Version < 1 {
		return empty, ErrInvalid
	}
	binding, err := m.studioAuthorizeOn(ctx, tx, project)
	if err != nil {
		return empty, err
	}
	row, err := m.studioRecordOn(ctx, tx, binding.ID, id)
	if err != nil {
		return empty, err
	}
	if row.Version != in.Version || row.State != "OPEN" {
		return empty, ErrConflict
	}
	q := query.BindStudioRecords(tx, m.names)
	updates := map[string]any{"version": row.Version + 1}
	switch in.Action {
	case "SELECT":
		if row.Kind != "CANDIDATE" || row.ParentID == nil {
			return empty, ErrInvalid
		}
		parent, e := m.studioRecordOn(ctx, tx, binding.ID, *row.ParentID)
		if e != nil {
			return empty, e
		}
		if parent.Kind != "SHOT" {
			return empty, ErrInvalid
		}
		_, err = q.WithContext(ctx).Where(q.ID.Eq(parent.ID), q.BindingID.Eq(binding.ID)).Updates(map[string]any{"selected_id": row.ID, "version": parent.Version + 1})
		if err != nil {
			return empty, err
		}
	case "CONFIRM":
		if row.Kind != "ASSISTANT_DRAFT" {
			return empty, ErrInvalid
		}
		view, e := studioView(row)
		if e != nil {
			return empty, e
		}
		if view.Content.OwnerID == nil || view.Content.DueAt == nil || view.Content.DueAt.IsZero() {
			return empty, ErrInvalid
		}
		if view.Content.DraftType == "STATUS" {
			return empty, ErrInvalid
		}
		owner := *view.Content.OwnerID
		// Reacquiring a lower-ID principal after the project fence would reverse
		// the global lock order. Owner eligibility is read here; the draft itself
		// remains a domain task record, not a Delivery assignment or execution.
		human, active, e := m.principals.ActiveHumanByID(ctx, tx, owner)
		if e != nil {
			return empty, e
		}
		if !active || human.ID != owner {
			return empty, ErrForbidden
		}
		updates["kind"] = "TASK"
		updates["state"] = "CONFIRMED"
		row.Kind = "TASK"
		row.State = "CONFIRMED"
	case "REJECT":
		if row.Kind != "ASSISTANT_DRAFT" {
			return empty, ErrInvalid
		}
		updates["state"] = "REJECTED"
		row.State = "REJECTED"
	case "RESOLVE":
		if row.Kind != "CONTINUITY" && row.Kind != "FORESHADOW" {
			return empty, ErrInvalid
		}
		updates["state"] = "RESOLVED"
		row.State = "RESOLVED"
	default:
		return empty, ErrInvalid
	}
	result, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.BindingID.Eq(binding.ID), q.Version.Eq(in.Version)).Updates(updates)
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrConflict
	}
	row.Version++
	if err = m.studioEventOn(ctx, tx, project, row, in.Action+":"+in.Reason); err != nil {
		return empty, err
	}
	return studioView(row)
}
func (m *Module) studioEventOn(ctx context.Context, tx *gorm.DB, project guid.ID, row *model.StudioRecord, action string) error {
	authority := "IAM:template_read+project_manage"
	event := audit.NewDomainEvent(ctx, row.TenantID, "template.studio_changed", "studio_record", row.ID)
	event.Detail["authority_source"] = authority
	event.Detail["project_id"] = project
	event.Detail["binding_id"] = row.BindingID
	event.Detail["action"] = action
	if row.Domain == "TEAM_TASKS" || row.Domain == "NOVEL" || row.Domain == "VIDEO" {
		event.Detail["record_payload"] = row.Payload
		event.Detail["record_version"] = row.Version
	}
	if err := m.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: project, Type: "template.studio_changed", Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: authority}, Subject: events.Subject{Kind: "studio_record", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"binding_id": row.BindingID, "domain": row.Domain, "kind": row.Kind, "state": row.State, "action": action}})
	return err
}
