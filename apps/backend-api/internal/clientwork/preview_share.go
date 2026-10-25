package clientwork

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const sharePurpose = "clientwork.preview_share"

var ErrShareEnded = errors.New("clientwork: preview share has ended")

type PreviewShareBody struct {
	PreviewID   guid.ID `json:"preview_id"`
	RecipientID guid.ID `json:"recipient_id"`
	TTLHours    int     `json:"ttl_hours" minimum:"1" maximum:"168"`
	Note        string  `json:"note" minLength:"1" maxLength:"500"`
}
type PreviewShareDecision struct {
	Decision string `json:"decision" enum:"APPROVE,REJECT,REVOKE"`
	Reason   string `json:"reason" minLength:"1" maxLength:"2000"`
}

// Share views disclose neither the source work/result nor approval identities.
// Link IDs locate records; every read still requires current project membership.
type PreviewShareView struct {
	ID        guid.ID      `json:"id"`
	PreviewID guid.ID      `json:"preview_id"`
	State     string       `json:"state"`
	Note      string       `json:"note"`
	ExpiresAt *time.Time   `json:"expires_at"`
	Version   int          `json:"version"`
	Preview   *PreviewView `json:"preview,omitempty"`
}

func shareRecord(r *model.PreviewShare) PreviewShareView {
	state := r.State
	if state == "ACTIVE" && (r.ExpiresAt == nil || !now().Before(*r.ExpiresAt)) {
		state = "EXPIRED"
	}
	return PreviewShareView{ID: r.ID, PreviewID: r.PreviewID, State: state, Note: r.Note, ExpiresAt: r.ExpiresAt, Version: r.Version}
}
func (m *Module) shareOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) (*model.PreviewShare, error) {
	q := query.BindPreviewShare(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).First()
}
func (m *Module) shareRecipientOn(ctx context.Context, tx *gorm.DB, p, recipient guid.ID) error {
	if recipient <= 0 {
		return ErrInvalid
	}
	if e := m.principals.Lock(ctx, tx, recipient); e != nil {
		return e
	}
	active, e := m.clients.IsClientOn(ctx, tx, authn.TenantIDFromContext(ctx), p, recipient)
	if e != nil {
		return e
	}
	if !active {
		return ErrNotFound
	}
	return nil
}
func (m *Module) requestShareOn(ctx context.Context, tx *gorm.DB, p guid.ID, in PreviewShareBody) (Receipt, error) {
	if in.PreviewID <= 0 || in.TTLHours < 1 || in.TTLHours > 168 || !validText(in.Note, 500) {
		return Receipt{}, ErrInvalid
	}
	if _, e := m.authorize(ctx, tx, p, "project_manage", false); e != nil {
		return Receipt{}, e
	}
	if e := m.shareRecipientOn(ctx, tx, p, in.RecipientID); e != nil {
		return Receipt{}, e
	}
	preview, e := m.previewOn(ctx, tx, p, in.PreviewID, false)
	if e != nil {
		return Receipt{}, e
	}
	// Public previews already have a separate, permanent visibility grant. An
	// expiring link must never imply that this permanent grant will expire too.
	if preview.ClientVisible {
		return Receipt{}, ErrConflict
	}
	q := query.BindPreviewShare(tx, m.names)
	current, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ActiveSlot.Eq(in.PreviewID)).First()
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return Receipt{}, e
	}
	if current != nil {
		if shareRecord(current).State != "EXPIRED" {
			return Receipt{}, ErrConflict
		}
		changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(current.TenantID), q.ID.Eq(current.ID), q.Version.Eq(current.Version), q.State.Eq("ACTIVE"), q.ActiveSlot.Eq(in.PreviewID)).UpdateSimple(q.State.Value("EXPIRED"), q.ActiveSlot.Null(), q.Version.Add(1))
		if e != nil {
			return Receipt{}, e
		}
		if changed.RowsAffected != 1 {
			return Receipt{}, ErrConflict
		}
		if e = m.event(ctx, tx, p, current.ID, "preview_share_ended", "project_manage", current.Version+1); e != nil {
			return Receipt{}, e
		}
	}
	id, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	row := &model.PreviewShare{TenantID: authn.TenantIDFromContext(ctx), ID: id, ProjectID: p, PreviewID: in.PreviewID, ActiveSlot: &in.PreviewID, RecipientID: in.RecipientID, RequestedBy: authn.PrincipalIDFromContext(ctx), State: "PENDING_APPROVAL", Note: in.Note, TTLHours: in.TTLHours, CreatedAt: now(), Version: 1}
	if e = q.WithContext(ctx).Create(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "preview_share_requested", "project_manage", 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: 1, State: row.State}, nil
}
func (m *Module) decideShareOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, version int, in PreviewShareDecision) (Receipt, error) {
	permission := "preview_share_approve"
	if in.Decision == "REVOKE" {
		permission = "project_manage"
	}
	if _, e := m.authorize(ctx, tx, p, permission, false); e != nil {
		return Receipt{}, e
	}
	if !validText(in.Reason, 2000) {
		return Receipt{}, ErrInvalid
	}
	row, e := m.shareOn(ctx, tx, p, id)
	if e != nil {
		return Receipt{}, e
	}
	if row.Version != version {
		return Receipt{}, ErrConflict
	}
	if shareRecord(row).State == "EXPIRED" {
		return Receipt{}, ErrShareEnded
	}
	actor := authn.PrincipalIDFromContext(ctx)
	at := now()
	event := "preview_share_decided"
	expectedState := row.State
	switch in.Decision {
	case "APPROVE", "REJECT":
		if row.State != "PENDING_APPROVAL" {
			return Receipt{}, ErrConflict
		}
		if row.RequestedBy == actor {
			return Receipt{}, ErrForbidden
		}
		if e = m.projects.ApprovePreviewShareOn(ctx, tx, row.TenantID, p); e != nil {
			return Receipt{}, e
		}
		row.DecidedBy = &actor
		row.DecidedAt = &at
		if in.Decision == "REJECT" {
			row.State = "REJECTED"
			row.ActiveSlot = nil
			break
		}
		if e = m.shareRecipientOn(ctx, tx, p, row.RecipientID); e != nil {
			return Receipt{}, e
		}
		preview, e := m.previewOn(ctx, tx, p, row.PreviewID, false)
		if e != nil {
			return Receipt{}, e
		}
		if preview.ClientVisible {
			return Receipt{}, ErrConflict
		}
		ref := delivery.ClientArtifactRef{WorkItemID: preview.WorkItemID, ResultID: preview.ResultID, EvidenceID: preview.EvidenceID}
		var candidate files.PinnedCandidate
		if preview.SourceStage == "PROGRESS" {
			if preview.SourceFileID != nil {
				_, candidate, e = m.projects.SourceArtifactOn(ctx, tx, row.TenantID, p, delivery.ClientSourceRef{WorkItemID: preview.WorkItemID, FileID: *preview.SourceFileID, VersionID: preview.FileVersionID, SHA256: preview.ContentHash})
			} else {
				_, candidate, e = m.projects.ProgressArtifactOn(ctx, tx, row.TenantID, p, ref)
			}
		} else {
			candidate, e = m.projects.ArtifactFileOn(ctx, tx, row.TenantID, p, ref)
		}
		if e != nil {
			return Receipt{}, e
		}
		if candidate.VersionID != preview.FileVersionID || candidate.SHA256 != preview.ContentHash {
			return Receipt{}, ErrConflict
		}
		row.State = "ACTIVE"
		expiry := at.Add(time.Duration(row.TTLHours) * time.Hour)
		row.ExpiresAt = &expiry
		// Persist the ACTIVE grant before invoking the canonical pin validator;
		// all writes and the binding remain inside the caller transaction.
		q := query.BindPreviewShare(tx, m.names)
		changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id), q.Version.Eq(version), q.State.Eq(expectedState)).UpdateSimple(q.State.Value(row.State), q.ExpiresAt.Value(expiry))
		if e != nil {
			return Receipt{}, e
		}
		if changed.RowsAffected != 1 {
			return Receipt{}, ErrConflict
		}
		expectedState = row.State
		if m.files.PinOn == nil {
			return Receipt{}, ErrUnavailable
		}
		binding, e := m.nextID()
		if e != nil {
			return Receipt{}, e
		}
		pin, e := m.files.PinOn(ctx, tx, files.ResourceBindingInput{ID: binding, SpaceID: candidate.SpaceID, FileID: candidate.FileID, ExpectedFileVersion: candidate.NodeVersion, VersionID: &candidate.VersionID, Purpose: sharePurpose, ResourceKind: "preview_share", ResourceID: id, Slot: "preview", ActorID: actor})
		if e != nil {
			return Receipt{}, e
		}
		row.BindingID = &pin.BindingID
	case "REVOKE":
		if row.State != "ACTIVE" && row.State != "PENDING_APPROVAL" {
			return Receipt{}, ErrConflict
		}
		row.State = "REVOKED"
		row.ActiveSlot = nil
		event = "preview_share_ended"
	default:
		return Receipt{}, ErrInvalid
	}
	row.Reason = &in.Reason
	row.Version++
	q := query.BindPreviewShare(tx, m.names)
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id), q.Version.Eq(version), q.State.Eq(expectedState)).Select(q.State, q.ActiveSlot, q.DecidedBy, q.DecidedAt, q.ExpiresAt, q.BindingID, q.Reason, q.Version).Updates(row)
	if e != nil {
		return Receipt{}, e
	}
	if changed.RowsAffected != 1 {
		return Receipt{}, ErrConflict
	}
	if e = m.event(ctx, tx, p, id, event, permission, row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: row.State}, nil
}
func (m *Module) authorizedShareOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) (*model.PreviewShare, error) {
	external, e := m.authorize(ctx, tx, p, "project_read", true)
	if e != nil {
		return nil, e
	}
	row, e := m.shareOn(ctx, tx, p, id)
	if e != nil {
		return nil, e
	}
	if external && row.RecipientID != authn.PrincipalIDFromContext(ctx) {
		return nil, ErrNotFound
	}
	if row.State != "ACTIVE" || row.ExpiresAt == nil || !now().Before(*row.ExpiresAt) {
		return nil, ErrShareEnded
	}
	// Invitation, Human lifecycle and project authority are rechecked on every
	// request, including pinned-file opens; possession of the link is no grant.
	if e = m.shareRecipientOn(ctx, tx, p, row.RecipientID); e != nil {
		return nil, e
	}
	return row, nil
}
func (m *Module) shareResourceOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	p := authn.EntityIDFromContext(ctx)
	if owner.Scope != "entity" || owner.ID != p {
		return ErrNotFound
	}
	_, e := m.authorizedShareOn(ctx, tx, p, id)
	return e
}
