package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("clientwork: invalid input")
	ErrNotFound    = errors.New("clientwork: not found")
	ErrConflict    = errors.New("clientwork: version or state conflict")
	ErrForbidden   = errors.New("clientwork: forbidden")
	ErrUnavailable = errors.New("clientwork: unavailable")
)

type SheetContent struct {
	Title      string                       `json:"title"`
	Items      []delivery.ClientRequirement `json:"items"`
	SourceRefs []string                     `json:"source_refs"`
	Milestones []string                     `json:"milestones"`
	OutOfScope []string                     `json:"out_of_scope"`
}
type SheetDraft struct {
	Title          string    `json:"title" minLength:"1" maxLength:"200"`
	RequirementIDs []guid.ID `json:"requirement_ids" minItems:"1" maxItems:"50"`
	SignerID       guid.ID   `json:"signer_id"`
	SourceRefs     []string  `json:"source_refs" maxItems:"50"`
	Milestones     []string  `json:"milestones" maxItems:"50"`
	OutOfScope     []string  `json:"out_of_scope" maxItems:"50"`
}
type SheetView struct {
	ID          guid.ID      `json:"id"`
	ProjectID   guid.ID      `json:"project_id"`
	Title       string       `json:"title"`
	Revision    int          `json:"revision"`
	Version     int          `json:"version"`
	State       string       `json:"state"`
	ContentHash string       `json:"content_hash"`
	SignerID    guid.ID      `json:"signer_id"`
	Content     SheetContent `json:"content"`
	Decision    *SignoffView `json:"decision,omitempty"`
}
type SignoffView struct {
	ID        guid.ID   `json:"id"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason"`
	ActorID   guid.ID   `json:"actor_id"`
	CreatedAt time.Time `json:"created_at"`
}
type SignoffBody struct {
	Revision    int    `json:"revision" minimum:"1"`
	ContentHash string `json:"content_hash" pattern:"^[a-f0-9]{64}$"`
	Decision    string `json:"decision" enum:"SIGN,REJECT"`
	Reason      string `json:"reason" minLength:"1" maxLength:"2000"`
}

func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }
func validText(s string, limit int) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit
}
func validStrings(values []string) bool {
	if len(values) > 50 {
		return false
	}
	for _, v := range values {
		if !validText(v, 2000) {
			return false
		}
	}
	return true
}

// authorize is also called before replay lookup. Old receipts never retain
// revoked account, project or client membership authority.
func (m *Module) authorize(ctx context.Context, tx *gorm.DB, project guid.ID, permission string, allowClient bool) (bool, error) {
	external, e := m.authorizeActorOn(ctx, tx, project, allowClient)
	if e != nil {
		return false, e
	}
	if external {
		permission = "channel_read"
	}
	if e = m.projects.LockProjectOn(ctx, tx, authn.TenantIDFromContext(ctx), project, permission); e != nil {
		return false, e
	}
	return external, nil
}

// authorizeActorOn fences the current Human, tenant and client identity. The
// caller must then lock and authorize the actual domain resource it will read.
func (m *Module) authorizeActorOn(ctx context.Context, tx *gorm.DB, project guid.ID, allowClient bool) (bool, error) {
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil || tenant <= 0 || actor <= 0 || project <= 0 || authn.EntityIDFromContext(ctx) != project {
		return false, ErrNotFound
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return false, ErrInvalid
	}
	if m == nil || m.projects == nil || m.clients == nil || m.principals.Lock == nil || m.members == nil || m.principals.ActiveHumanByID == nil {
		return false, ErrUnavailable
	}
	if e := m.principals.Lock(ctx, tx, actor); e != nil {
		return false, e
	}
	h, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return false, e
	}
	if !active || h.ID != actor {
		return false, ErrForbidden
	}
	active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if e != nil {
		return false, e
	}
	if !active {
		return false, ErrNotFound
	}
	external, e := m.clients.IsExternalOn(ctx, tx, tenant, actor)
	if e != nil {
		return false, e
	}
	if external {
		active, e := m.clients.IsClientOn(ctx, tx, tenant, project, actor)
		if e != nil {
			return false, e
		}
		if !active {
			return false, ErrNotFound
		}
		if !allowClient {
			return false, ErrNotFound
		}
	}
	return external, nil
}
func (m *Module) nextID() (guid.ID, error) {
	if m.ids == nil {
		return 0, ErrUnavailable
	}
	v, e := m.ids.Next()
	if e != nil {
		return 0, e
	}
	if v <= 0 {
		return 0, ErrUnavailable
	}
	return guid.ID(v), nil
}
func (m *Module) event(ctx context.Context, tx *gorm.DB, project, id guid.ID, kind, permission string, version int) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	e := audit.NewDomainEvent(ctx, tenant, "clientwork."+kind, "clientwork", id)
	e.Detail["project_id"] = project.String()
	e.Detail["version"] = version
	e.Detail["authority_source"] = "permission:" + permission
	data := map[string]any{"version": version}
	eventActor := events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:" + permission}
	if kind == "package_decided" {
		q := query.BindPackageReceipt(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.PackageID.Eq(id)).Take()
		if err != nil {
			return err
		}
		if r.Source == "RECORDED" {
			eventActor = events.Actor{Kind: "CLIENT", ID: r.ActorID, AuthoritySource: "permission:delivery_send"}
			e.Detail["source"] = "RECORDED"
			e.Detail["recorded_by"] = r.RecordedBy
			data["source"] = "RECORDED"
			data["recorded_by"] = r.RecordedBy
		}
		if r.ReceiptSHA256 != nil {
			e.Detail["receipt_sha256"] = *r.ReceiptSHA256
			data["receipt_sha256"] = *r.ReceiptSHA256
		}
	}
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "clientwork." + kind, Actor: eventActor, Subject: events.Subject{Kind: "clientwork", ID: id}, SubjectVersion: int64(version), Data: data})
	if err != nil {
		return err
	}
	if kind == "package_published" {
		if err := m.publishSubmissionDeliveriesOn(ctx, tx, project, id); err != nil {
			return err
		}
		if err := m.scheduleAcceptanceRemindersOn(ctx, tx, project, id); err != nil {
			return err
		}
	}
	if kind == "package_drafted" || kind == "package_published" || kind == "package_decided" || kind == "package_archived" {
		return m.recordPackageTimelineOn(ctx, tx, project, id, kind, version)
	}
	return nil
}
func (m *Module) loadSheet(ctx context.Context, tx *gorm.DB, project, id guid.ID) (*model.Sheet, error) {
	q := query.BindSheets(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return r, e
}
func (m *Module) sheetView(ctx context.Context, tx *gorm.DB, row *model.Sheet, external bool, revision int) (SheetView, error) {
	var out SheetView
	if revision == 0 {
		revision = row.CurrentRevision
		if external {
			revision = row.PublishedRevision
		}
	}
	if revision <= 0 || revision > row.CurrentRevision || (external && revision > row.PublishedRevision) {
		return out, ErrNotFound
	}
	q := query.BindRevisions(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.SheetID.Eq(row.ID), q.Revision.Eq(revision)).First()
	if e != nil {
		return out, e
	}
	out = SheetView{ID: row.ID, ProjectID: row.ProjectID, Title: row.Title, Revision: revision, Version: row.Version, State: row.State, ContentHash: r.ContentHash, SignerID: r.SignerID}
	if external && revision != row.CurrentRevision {
		out.State = "AWAITING_SIGNOFF"
		out.Version = 0
	}
	if e = json.Unmarshal([]byte(r.Content), &out.Content); e != nil {
		return out, e
	}
	out.Title = out.Content.Title
	d := query.BindSignoffs(tx, m.names)
	decision, e := d.WithContext(ctx).Where(d.TenantID.Eq(row.TenantID), d.ProjectID.Eq(row.ProjectID), d.RevisionID.Eq(r.ID)).First()
	if e == nil {
		out.Decision = &SignoffView{decision.ID, decision.Decision, decision.Reason, decision.ActorID, decision.CreatedAt}
		if decision.Decision == "SIGN" {
			out.State = "SIGNED"
		} else {
			out.State = "REJECTED"
		}
	} else if !errors.Is(e, gorm.ErrRecordNotFound) {
		return out, e
	}
	return out, nil
}
func (m *Module) draftSheetOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, expected int, in SheetDraft) (SheetView, error) {
	var out SheetView
	if !validText(in.Title, 200) || !validStrings(in.SourceRefs) || !validStrings(in.Milestones) || !validStrings(in.OutOfScope) || in.SignerID <= 0 {
		return out, ErrInvalid
	}
	if _, e := m.authorize(ctx, tx, project, "requirement_write", false); e != nil {
		return out, e
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	external, e := m.clients.IsClientOn(ctx, tx, tenant, project, in.SignerID)
	if e != nil {
		return out, e
	}
	if !external {
		return out, ErrNotFound
	}
	items, e := m.projects.RequirementsOn(ctx, tx, tenant, project, in.RequirementIDs)
	if e != nil {
		return out, e
	}
	content := SheetContent{Title: in.Title, Items: items, SourceRefs: in.SourceRefs, Milestones: in.Milestones, OutOfScope: in.OutOfScope}
	data, e := json.Marshal(content)
	if e != nil {
		return out, e
	}
	if len(data) > 240000 {
		return out, ErrInvalid
	}
	digest := sha256.Sum256(data)
	q := query.BindSheets(tx, m.names)
	var row *model.Sheet
	if id == 0 {
		id, e = m.nextID()
		if e != nil {
			return out, e
		}
		row = &model.Sheet{TenantID: tenant, ID: id, ProjectID: project, Title: in.Title, CurrentRevision: 1, State: "DRAFT", Version: 1, CreatedBy: actor, CreatedAt: now(), UpdatedAt: now()}
		if e = q.WithContext(ctx).Create(row); e != nil {
			return out, e
		}
	} else {
		row, e = m.loadSheet(ctx, tx, project, id)
		if e != nil {
			return out, e
		}
		if row.Version != expected || expected < 1 {
			return out, ErrConflict
		}
		row.CurrentRevision++
		row.Version++
		row.Title = in.Title
		row.State = "DRAFT"
		row.UpdatedAt = now()
		if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Select(q.CurrentRevision, q.Version, q.State, q.Title, q.UpdatedAt).Updates(row); e != nil {
			return out, e
		}
	}
	rid, e := m.nextID()
	if e != nil {
		return out, e
	}
	r := &model.SheetRevision{TenantID: tenant, ID: rid, ProjectID: project, SheetID: id, Revision: row.CurrentRevision, Content: string(data), ContentHash: hex.EncodeToString(digest[:]), SignerID: in.SignerID, CreatedBy: actor, CreatedAt: now()}
	if e = query.BindRevisions(tx, m.names).WithContext(ctx).Create(r); e != nil {
		return out, e
	}
	if e = m.event(ctx, tx, project, id, "sheet_drafted", "requirement_write", row.Version); e != nil {
		return out, e
	}
	return m.sheetView(ctx, tx, row, false, 0)
}
func (m *Module) publishSheetOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, expected int) (SheetView, error) {
	var out SheetView
	if _, e := m.authorize(ctx, tx, project, "requirement_write", false); e != nil {
		return out, e
	}
	row, e := m.loadSheet(ctx, tx, project, id)
	if e != nil {
		return out, e
	}
	if row.Version != expected || row.State != "DRAFT" {
		return out, ErrConflict
	}
	v, e := m.sheetView(ctx, tx, row, false, 0)
	if e != nil {
		return out, e
	}
	active, e := m.clients.IsClientOn(ctx, tx, row.TenantID, project, v.SignerID)
	if e != nil {
		return out, e
	}
	if !active {
		return out, ErrNotFound
	}
	ids := make([]guid.ID, len(v.Content.Items))
	for i, item := range v.Content.Items {
		ids[i] = item.ID
	}
	current, e := m.projects.RequirementsOn(ctx, tx, row.TenantID, project, ids)
	if e != nil {
		return out, e
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(v.Content.Items)
	if string(a) != string(b) {
		return out, ErrConflict
	}
	q := query.BindSheets(tx, m.names)
	row.State = "AWAITING_SIGNOFF"
	row.PublishedRevision = row.CurrentRevision
	row.Version++
	row.UpdatedAt = now()
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.State, q.PublishedRevision, q.Version, q.UpdatedAt).Updates(row); e != nil {
		return out, e
	}
	if e = m.event(ctx, tx, project, id, "sheet_published", "requirement_write", row.Version); e != nil {
		return out, e
	}
	return m.sheetView(ctx, tx, row, false, 0)
}
func (m *Module) signSheetOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, expected int, in SignoffBody) (SheetView, error) {
	var out SheetView
	if !validText(in.Reason, 2000) || (in.Decision != "SIGN" && in.Decision != "REJECT") {
		return out, ErrInvalid
	}
	external, e := m.authorize(ctx, tx, project, "requirement_read", true)
	if e != nil {
		return out, e
	}
	if !external {
		return out, ErrForbidden
	}
	if _, e = m.clientSignerOn(ctx, tx, project); e != nil {
		return out, e
	}
	row, e := m.loadSheet(ctx, tx, project, id)
	if e != nil {
		return out, e
	}
	if row.Version != expected || row.State != "AWAITING_SIGNOFF" || row.CurrentRevision != in.Revision || row.PublishedRevision != in.Revision {
		return out, ErrConflict
	}
	v, e := m.sheetView(ctx, tx, row, true, 0)
	if e != nil {
		return out, e
	}
	if v.ContentHash != in.ContentHash {
		return out, ErrConflict
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if v.SignerID != actor {
		return out, ErrNotFound
	}
	if in.Decision == "SIGN" {
		if e = m.projects.SignRequirementsOn(ctx, tx, row.TenantID, project, v.Content.Items); e != nil {
			return out, e
		}
		row.State = "SIGNED"
	} else {
		row.State = "REJECTED"
	}
	r := query.BindRevisions(tx, m.names)
	rev, e := r.WithContext(ctx).Where(r.TenantID.Eq(row.TenantID), r.SheetID.Eq(id), r.Revision.Eq(in.Revision)).First()
	if e != nil {
		return out, e
	}
	did, e := m.nextID()
	if e != nil {
		return out, e
	}
	decision := &model.Signoff{TenantID: row.TenantID, ID: did, ProjectID: project, SheetID: id, RevisionID: rev.ID, Decision: in.Decision, Reason: in.Reason, ContentHash: in.ContentHash, ActorID: actor, CreatedAt: now()}
	if e = query.BindSignoffs(tx, m.names).WithContext(ctx).Create(decision); e != nil {
		return out, e
	}
	row.Version++
	row.UpdatedAt = now()
	q := query.BindSheets(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.State, q.Version, q.UpdatedAt).Updates(row); e != nil {
		return out, e
	}
	if e = m.event(ctx, tx, project, id, "sheet_decided", "channel_read", row.Version); e != nil {
		return out, e
	}
	return m.sheetView(ctx, tx, row, true, 0)
}
