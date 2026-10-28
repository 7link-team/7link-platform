package intake

import (
	activityi18n "7link.com/platform/internal/intake/i18n"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrForbidden = errors.New("intake: forbidden")
var ErrConflict = errors.New("intake: signed sheet conflict")

type BriefItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type SignedBriefInput struct {
	TenantID  guid.ID     `json:"tenant_id"`
	ProjectID guid.ID     `json:"project_id"`
	BindingID guid.ID     `json:"binding_id"`
	IntakeID  guid.ID     `json:"intake_id"`
	Items     []BriefItem `json:"items"`
}
type SignedBriefFact struct {
	ID guid.ID `json:"id"`
	SignedBriefInput
	Hash     string    `json:"hash"`
	SignedBy guid.ID   `json:"signed_by"`
	SignedAt time.Time `json:"signed_at"`
}

// The owning workflow holds the project/binding fence, and checks project read
// authority before ReadOn. SignOn independently checks current Human signing rights.
type SignedBriefs struct {
	SignOn func(context.Context, *gorm.DB, SignedBriefInput) (SignedBriefFact, error)
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (SignedBriefFact, bool, error)
}
type signedBriefServices struct {
	ids        guid.Source
	principals identity.PrincipalStateStore
	explain    core.AuthorizationExplanation
	audit      audit.Appender
	events     events.Publisher
}

func (*Module) ModuleDependencies() []string {
	return []string{identity.Name, core.Name, guid.Name, audit.Name, events.Name}
}
func (m *Module) InjectServices(s *module.Services) error {
	var e error
	if m.brief.ids, e = module.Resolve[guid.Source](s); e != nil {
		return e
	}
	if m.brief.principals, e = module.Resolve[identity.PrincipalStateStore](s); e != nil {
		return e
	}
	if m.brief.explain, e = module.Resolve[core.AuthorizationExplanation](s); e != nil {
		return e
	}
	if m.brief.audit, e = module.Resolve[audit.Appender](s); e != nil {
		return e
	}
	if m.brief.events, e = module.Resolve[events.Publisher](s); e != nil {
		return e
	}
	catalog, e := module.Resolve[events.Catalog](s)
	if e != nil {
		return e
	}
	for _, kind := range []string{"intake.clarification_requested", "intake.clarification_answered", "intake.clarification_cancelled", "intake.delivery_published"} { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		if err := catalog.AddTypes(Name, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_" + strings.ReplaceAll(kind, ".", "_") + "_name", DescriptionKey: activityi18n.Namespace + ".activity_event_" + strings.ReplaceAll(kind, ".", "_") + "_description", Name: kind, SchemaVersion: 1, SubjectKind: "intake", Realtime: events.RealtimeNone}); err != nil {
			return err
		}
	}
	return catalog.AddTypes(Name, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_intake_triage_requested_name", DescriptionKey: activityi18n.Namespace + ".activity_event_intake_triage_requested_description", Name: "intake.triage_requested", SchemaVersion: 1, SubjectKind: "intake", Realtime: events.RealtimeNone}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_intake_merged_name", DescriptionKey: activityi18n.Namespace + ".activity_event_intake_merged_description", Name: "intake.merged", SchemaVersion: 1, SubjectKind: "intake", Realtime: events.RealtimeNone}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_intake_declined_name", DescriptionKey: activityi18n.Namespace + ".activity_event_intake_declined_description", Name: "intake.declined", SchemaVersion: 1, SubjectKind: "intake", Realtime: events.RealtimeNone}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_intake_brief_signed_name", DescriptionKey: activityi18n.Namespace + ".activity_event_intake_brief_signed_description", Name: "intake.brief_signed", SchemaVersion: 1, SubjectKind: "signed_brief", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_intake_triaged_name", DescriptionKey: activityi18n.Namespace + ".activity_event_intake_triaged_description", Name: "intake.triaged", SchemaVersion: 1, SubjectKind: "intake", Realtime: events.RealtimeNone}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_intake_converted_name", DescriptionKey: activityi18n.Namespace + ".activity_event_intake_converted_description", Name: "intake.converted", SchemaVersion: 1, SubjectKind: "intake", Realtime: events.RealtimeNone}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_intake_received_name", DescriptionKey: activityi18n.Namespace + ".activity_event_intake_received_description", Name: "intake.received", SchemaVersion: 1, SubjectKind: "intake", Realtime: events.RealtimeNone})
}
func briefFact(r *model.SignedBrief) (SignedBriefFact, error) {
	out := SignedBriefFact{ID: r.ID, SignedBriefInput: SignedBriefInput{TenantID: r.TenantID, ProjectID: r.ProjectID, BindingID: r.BindingID, IntakeID: r.IntakeID}, Hash: r.SHA256, SignedBy: r.SignedBy, SignedAt: r.SignedAt}
	e := json.Unmarshal([]byte(r.ItemsJSON), &out.Items)
	return out, e
}
func (m *Module) readSignedBriefOn(ctx context.Context, tx *gorm.DB, tenant, project, binding guid.ID) (SignedBriefFact, bool, error) {
	var out SignedBriefFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || binding <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return out, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalidArgument
	}
	q := query.BindSignedBriefs(tx, m.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.BindingID.Eq(binding)).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	out, e = briefFact(row)
	return out, true, e
}
func (m *Module) signBriefOn(ctx context.Context, tx *gorm.DB, in SignedBriefInput) (SignedBriefFact, error) {
	var out SignedBriefFact
	if in.TenantID <= 0 || in.ProjectID <= 0 || in.BindingID <= 0 || in.IntakeID <= 0 || len(in.Items) < 1 || len(in.Items) > 1000 {
		return out, ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, item := range in.Items {
		if strings.TrimSpace(item.ID) == "" || utf8.RuneCountInString(item.ID) > 128 || strings.TrimSpace(item.Text) == "" || utf8.RuneCountInString(item.Text) > 4000 || seen[item.ID] {
			return out, ErrInvalidArgument
		}
		seen[item.ID] = true
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if authn.TenantIDFromContext(ctx) != in.TenantID || actor <= 0 {
		return out, ErrForbidden
	}
	if tx == nil || tx.Statement == nil {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if m.brief.ids == nil || m.brief.principals.Lock == nil || m.brief.principals.ActiveHumanByID == nil || m.brief.explain == nil || m.brief.audit == nil || m.brief.events == nil {
		return out, ErrUnavailable
	}
	if e := m.brief.principals.Lock(ctx, tx, actor); e != nil {
		return out, e
	}
	if p, active, e := m.brief.principals.ActiveHumanByID(ctx, tx, actor); e != nil {
		return out, e
	} else if !active || p.ID != actor {
		return out, ErrForbidden
	}
	explanation, e := m.brief.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: in.TenantID, EntityID: in.ProjectID, PrincipalID: actor, Permission: "brief_sign"})
	if e != nil {
		return out, e
	}
	if !explanation.Allowed || explanation.Reason == "explicit_deny" || explanation.Reason == "administrator_scope_denied" {
		return out, ErrForbidden
	}
	// A sheet must reference a genuine source already converted into this project.
	q := query.BindRecords(tx, m.names)
	source, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(in.TenantID), q.ProjectID.Eq(in.ProjectID), q.ID.Eq(in.IntakeID), q.Status.Eq("CONVERTED")).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, ErrInvalidArgument
	}
	if e != nil {
		return out, e
	}
	_ = source
	raw, e := json.Marshal(in.Items)
	if e != nil {
		return out, e
	}
	if len(raw) > 1048576 {
		return out, ErrInvalidArgument
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	prior, found, e := m.readSignedBriefOn(ctx, tx, in.TenantID, in.ProjectID, in.BindingID)
	if e != nil {
		return out, e
	}
	if found {
		if prior.Hash != digest || prior.IntakeID != in.IntakeID || prior.SignedBy != actor {
			return out, ErrConflict
		}
		return prior, nil
	}
	id, e := m.brief.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	row := &model.SignedBrief{ID: guid.ID(id), TenantID: in.TenantID, ProjectID: in.ProjectID, IntakeID: in.IntakeID, BindingID: in.BindingID, ItemsJSON: string(raw), SHA256: digest, SignedBy: actor, SignedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if e = query.BindSignedBriefs(tx, m.names).WithContext(ctx).Create(row); e != nil {
		return out, e
	}
	event := audit.NewDomainEvent(ctx, in.TenantID, "intake.brief_signed", "signed_brief", row.ID)
	event.Detail["authority_source"] = "IAM:brief_sign"
	event.Detail["binding_id"] = in.BindingID.String()
	event.Detail["hash"] = digest
	event.Detail["intake_id"] = in.IntakeID.String()
	if e = m.brief.audit(ctx, tx, event); e != nil {
		return out, e
	}
	_, e = m.brief.events.Publish(ctx, tx, events.Event{TenantID: in.TenantID, ProjectID: in.ProjectID, Type: "intake.brief_signed", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "IAM:brief_sign"}, Subject: events.Subject{Kind: "signed_brief", ID: row.ID}, SubjectVersion: 1, Data: map[string]any{"binding_id": in.BindingID.String(), "hash": digest, "intake_id": in.IntakeID.String()}})
	if e != nil {
		return out, e
	}
	return briefFact(row)
}

var _ module.Depender = (*Module)(nil)
var _ module.ServiceInjector = (*Module)(nil)
