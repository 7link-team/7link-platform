package clientwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const releaseNotePath = entityBase + "/delivery-packages/{id}/release-note-drafts"

type ReleaseNoteRequest struct {
	Note string `json:"note" minLength:"1" maxLength:"2000"`
}
type ReleaseNoteText struct {
	SourceFingerprint string `json:"source_fingerprint" minLength:"64" maxLength:"64" pattern:"^[a-f0-9]{64}$"`
	Content           string `json:"content" minLength:"1" maxLength:"8000"`
	PackageVersion    int    `json:"package_version" minimum:"1"`
}
type ReleaseNoteInput struct {
	EditInput[ReleaseNoteText]
	DraftID string `path:"draft_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// A distinct type permits only this Agent command through the Human command fence.
type ReleaseNoteWriteInput ReleaseNoteInput

func (m *Module) registerReleaseNoteHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.owner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.release_note.request", Method: "POST", Path: releaseNotePath, Summary: "Request an Agent release note from frozen package sources", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "compose"}, m.requestReleaseNoteHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.release_note.list", Method: "GET", Path: releaseNotePath, Summary: "List private frozen release note sources and drafts", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "read"}, m.listReleaseNotesHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.release_note.write", Method: "POST", Path: releaseNotePath + "/{draft_id}/content", Summary: "Submit the selected Agent's release note draft", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "read"}, m.writeReleaseNoteHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.release_note.adopt", Method: "POST", Path: releaseNotePath + "/{draft_id}/adoption", Summary: "Adopt a release note with actual Human final edits", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "compose"}, m.adoptReleaseNoteHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	for _, path := range []string{releaseNotePath, releaseNotePath + "/{draft_id}/content", releaseNotePath + "/{draft_id}/adoption"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func releaseSnapshot(row *model.DeliveryPackage) string {
	raw, _ := json.Marshal(struct {
		Version   int     `json:"version"`
		Recipient guid.ID `json:"recipient_id"`
		Manifest  string  `json:"manifest"`
	}{row.Version, row.RecipientID, row.Manifest})
	return string(raw)
}
func releaseFingerprint(manifest string) string {
	sum := sha256.Sum256([]byte(manifest))
	return hex.EncodeToString(sum[:])
}
func (m *Module) releaseSourceOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, version int) (*model.DeliveryPackage, error) {
	row, err := m.packageOn(ctx, tx, p, id, false)
	if err != nil {
		return nil, err
	}
	if row.State != "DRAFT" || row.ArchivedAt != nil || row.Version != version {
		return nil, ErrConflict
	}
	return row, nil
}
func (m *Module) releaseDraftOn(ctx context.Context, tx *gorm.DB, p, pkg, id guid.ID) (*model.ReleaseNoteDraft, error) {
	q := query.BindReleaseNoteDraft(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.PackageID.Eq(pkg), q.ID.Eq(id)).Take()
}
func (m *Module) releaseEventOn(ctx context.Context, tx *gorm.DB, p guid.ID, row *model.ReleaseNoteDraft, kind, actorKind string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	permission := "delivery_compose"
	if actorKind == "AGENT" {
		permission = "delivery_read"
	}
	e := audit.NewDomainEvent(ctx, row.TenantID, "clientwork."+kind, Name, row.ID)
	e.Detail["authority_source"] = "permission:" + permission
	e.Detail["actor_kind"] = actorKind
	e.Detail["package_id"] = row.PackageID.String()
	e.Detail["source_fingerprint"] = row.SourceFingerprint
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: p, Type: "clientwork." + kind, Actor: events.Actor{Kind: actorKind, ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "permission:" + permission}, Subject: events.Subject{Kind: Name, ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"package_id": row.PackageID.String(), "source_fingerprint": row.SourceFingerprint}})
	return err
}
func (m *Module) requestReleaseNoteHTTP(ctx context.Context, in *EditInput[ReleaseNoteRequest]) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.writer() == nil || m.releaseDrafters.SelectOn == nil || m.releaseDrafters.AuthorizeOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var agent guid.ID
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		agent, e = m.releaseDrafters.SelectOn(ctx, tx, authn.TenantIDFromContext(ctx), p)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return m.command(ctx, p, "clientwork.release_note.request", "POST", releaseNotePath, in.Key, 201, in, "delivery_compose", false, func(tx *gorm.DB) error {
		return m.releaseDrafters.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), p, agent)
	}, func(tx *gorm.DB) (Receipt, error) {
		if !validText(in.Body.Note, 2000) || m.triageInbox.AppendOn == nil {
			return Receipt{}, ErrInvalid
		}
		pkg, e := m.releaseSourceOn(ctx, tx, p, id, v)
		if e != nil {
			return Receipt{}, e
		}
		q := query.BindReleaseNoteDraft(tx, m.names)
		n, e := q.WithContext(ctx).Where(q.TenantID.Eq(pkg.TenantID), q.PackageID.Eq(id)).Count()
		if e != nil {
			return Receipt{}, e
		}
		if n >= 20 {
			return Receipt{}, ErrConflict
		}
		draft, e := m.nextID()
		if e != nil {
			return Receipt{}, e
		}
		row := &model.ReleaseNoteDraft{TenantID: pkg.TenantID, ID: draft, ProjectID: p, PackageID: id, SourceSnapshot: releaseSnapshot(pkg), SourceFingerprint: releaseFingerprint(releaseSnapshot(pkg)), RequestedBy: authn.PrincipalIDFromContext(ctx), DrafterPrincipalID: agent, Note: in.Body.Note, State: "REQUESTED", Version: 1, CreatedAt: now(), UpdatedAt: now()}
		if e = q.WithContext(ctx).Create(row); e != nil {
			return Receipt{}, e
		}
		if _, e = m.triageInbox.AppendOn(ctx, tx, pkg.TenantID, agent, agents.InboxReference{SourceEventID: "release-note:" + draft.String(), Kind: "STATE", SubjectID: draft}); e != nil {
			return Receipt{}, e
		}
		e = m.releaseEventOn(ctx, tx, p, row, "release_note_requested", "HUMAN")
		return Receipt{ID: draft, Version: 1, State: row.State}, e
	}, func(tx *gorm.DB) error { return m.lockRecordedParticipantsOn(ctx, tx, agent) })
}
func (m *Module) listReleaseNotesHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[[]*model.ReleaseNoteDraft], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var rows []*model.ReleaseNoteDraft
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Human or selected current project Agent only; never expose private sources to clients.
		if _, e := m.authorize(ctx, tx, p, "delivery_compose", false); e != nil {
			if m.releaseDrafters.WriterOn == nil {
				return e
			}
			if e = m.releaseDrafters.WriterOn(ctx, tx, authn.TenantIDFromContext(ctx), p); e != nil {
				return e
			}
			q := query.BindReleaseNoteDraft(tx, m.names)
			var e error
			rows, e = q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.PackageID.Eq(id), q.DrafterPrincipalID.Eq(authn.PrincipalIDFromContext(ctx))).Order(q.ID.Desc()).Limit(20).Find()
			return e
		}
		q := query.BindReleaseNoteDraft(tx, m.names)
		var e error
		rows, e = q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.PackageID.Eq(id)).Order(q.ID.Desc()).Limit(20).Find()
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, rows), nil
}
func (m *Module) writeReleaseNoteHTTP(ctx context.Context, in *ReleaseNoteWriteInput) (*CommandOutput, error) {
	return m.changeReleaseNoteHTTP(ctx, (*ReleaseNoteInput)(in), in, false)
}
func (m *Module) adoptReleaseNoteHTTP(ctx context.Context, in *ReleaseNoteInput) (*CommandOutput, error) {
	return m.changeReleaseNoteHTTP(ctx, in, in, true)
}
func (m *Module) changeReleaseNoteHTTP(ctx context.Context, in *ReleaseNoteInput, request any, adopt bool) (*CommandOutput, error) {
	p, pkg, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	id, err := guid.RequireID(in.DraftID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	op, path := "clientwork.release_note.write", releaseNotePath+"/"+id.String()+"/content"
	if adopt {
		op, path = "clientwork.release_note.adopt", releaseNotePath+"/"+id.String()+"/adoption"
	}
	return m.command(ctx, p, op, "POST", path, in.Key, 200, request, "delivery_compose", false, func(tx *gorm.DB) error {
		row, e := m.releaseDraftOn(ctx, tx, p, pkg, id)
		if e != nil {
			return e
		}
		if !adopt && (row.DrafterPrincipalID != authn.PrincipalIDFromContext(ctx)) {
			return ErrForbidden
		}
		return nil
	}, func(tx *gorm.DB) (Receipt, error) {
		if !validText(in.Body.Content, 8000) {
			return Receipt{}, ErrInvalid
		}
		row, e := m.releaseDraftOn(ctx, tx, p, pkg, id)
		if e != nil {
			return Receipt{}, e
		}
		source, e := m.releaseSourceOn(ctx, tx, p, pkg, in.Body.PackageVersion)
		if e != nil {
			return Receipt{}, e
		}
		if row.Version != v || row.SourceFingerprint != in.Body.SourceFingerprint || row.SourceFingerprint != releaseFingerprint(row.SourceSnapshot) || row.SourceSnapshot != releaseSnapshot(source) {
			return Receipt{}, ErrConflict
		}
		kind, actorKind := "release_note_drafted", "AGENT"
		if adopt {
			if row.State != "DRAFT" || row.Content == nil || strings.TrimSpace(*row.Content) == strings.TrimSpace(in.Body.Content) {
				return Receipt{}, ErrConflict
			}
			var manifest packageManifest
			if e = json.Unmarshal([]byte(source.Manifest), &manifest); e != nil {
				return Receipt{}, e
			}
			if _, e = m.editPackageOn(ctx, tx, p, pkg, source.Version, PackageBody{Title: source.Title, ReleaseNotes: in.Body.Content, RecipientID: source.RecipientID, Artifacts: manifest.Refs}); e != nil {
				return Receipt{}, e
			}
			actor := authn.PrincipalIDFromContext(ctx)
			row.AdoptedBy = &actor
			row.State = "ADOPTED"
			kind, actorKind = "release_note_adopted", "HUMAN"
		} else {
			if row.State != "REQUESTED" {
				return Receipt{}, ErrConflict
			}
			row.Content = &in.Body.Content
			row.State = "DRAFT"
		}
		row.Version++
		row.UpdatedAt = now()
		q := query.BindReleaseNoteDraft(tx, m.names)
		if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID)).Select(q.Content, q.State, q.AdoptedBy, q.Version, q.UpdatedAt).Updates(row); e != nil {
			return Receipt{}, e
		}
		e = m.releaseEventOn(ctx, tx, p, row, kind, actorKind)
		return Receipt{ID: row.ID, Version: row.Version, State: row.State}, e
	})
}
