package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type PackageSendBody struct {
	ChannelID guid.ID `json:"channel_id"`
}
type PackageSendView struct {
	ID             guid.ID   `json:"id"`
	PackageVersion int       `json:"package_version"`
	ContentHash    string    `json:"content_hash"`
	RecipientID    guid.ID   `json:"recipient_id"`
	ChannelID      guid.ID   `json:"channel_id"`
	Attempt        int       `json:"attempt_no"`
	State          string    `json:"state" enum:"FAILED,SUCCEEDED"`
	MessageID      *guid.ID  `json:"message_id"`
	FailureCode    *string   `json:"failure_code"`
	ActorID        guid.ID   `json:"actor_id"`
	CreatedAt      time.Time `json:"created_at"`
}

const packageSendsPath = entityBase + "/delivery-packages/{id}/sends"

func (m *Module) registerPackageSendsHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.owner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.sends", Method: "GET", Path: packageSendsPath, Summary: "Read actual recipient channel send attempts", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "send"}, m.packageSendsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.send", Method: "POST", Path: packageSendsPath, Summary: "Send or retry a published package to its recipient shared channel", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "send"}, m.packageSendHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, packageSendsPath)
}

// Current recipient and channel authority is checked even for exact replay.
func (m *Module) packageSendAuthorityOn(ctx context.Context, tx *gorm.DB, p, id, channel, recipient guid.ID) (*model.DeliveryPackage, error) {
	row, err := m.packageOn(ctx, tx, p, id, false)
	if err != nil {
		return nil, err
	}
	if row.RecipientID != recipient {
		return nil, ErrConflict
	}
	active, err := m.clients.IsClientOn(ctx, tx, row.TenantID, p, recipient)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrNotFound
	}
	if m.packageRecipient == nil {
		return nil, ErrUnavailable
	}
	if err = m.packageRecipient(ctx, tx, row.TenantID, p, recipient); err != nil {
		return nil, err
	}
	if m.packageSender.CheckOn == nil {
		return nil, ErrUnavailable
	}
	if err = m.packageSender.CheckOn(ctx, tx, row.TenantID, p, channel, recipient); err != nil {
		return nil, err
	}
	return row, nil
}
func (m *Module) packageSendHTTP(ctx context.Context, in *EditInput[PackageSendBody]) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil || in.Body.ChannelID <= 0 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	// Read solely to establish stable principal locking order. The transaction
	// re-reads the aggregate and refuses a changed recipient before any effect.
	q := query.BindDeliveryPackage(m.writer(), m.names)
	candidate, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).Take()
	if err != nil {
		return nil, httpError(ctx, ErrNotFound)
	}
	return m.command(ctx, p, "clientwork.package.send", "POST", packageSendsPath, in.Key, 200, in, "delivery_send", false, func(tx *gorm.DB) error {
		_, err := m.packageSendAuthorityOn(ctx, tx, p, id, in.Body.ChannelID, candidate.RecipientID)
		return err
	}, func(tx *gorm.DB) (Receipt, error) {
		return m.sendPackageOn(ctx, tx, p, id, v, in.Body.ChannelID, candidate.RecipientID)
	}, func(tx *gorm.DB) error { return m.lockRecordedParticipantsOn(ctx, tx, candidate.RecipientID) })
}
func (m *Module) sendPackageOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, channel, recipient guid.ID) (Receipt, error) {
	row, err := m.packageSendAuthorityOn(ctx, tx, p, id, channel, recipient)
	if err != nil {
		return Receipt{}, err
	}
	if row.State != "PUBLISHED" || row.ArchivedAt != nil || row.Version != v {
		return Receipt{}, ErrConflict
	}
	q := query.BindPackageSend(tx, m.names)
	prior, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(row.TenantID), q.PackageID.Eq(id)).Order(q.Attempt.Desc()).Limit(1).Find()
	if err != nil {
		return Receipt{}, err
	}
	attempt := 1
	if len(prior) > 0 {
		last := prior[0]
		if last.State == "SUCCEEDED" || last.ContentHash != row.ContentHash || last.RecipientID != recipient || last.ChannelID != channel {
			return Receipt{}, ErrConflict
		}
		attempt = last.Attempt + 1
	}
	sid, err := m.nextID()
	if err != nil {
		return Receipt{}, err
	}
	entry := &model.PackageSend{TenantID: row.TenantID, ID: sid, ProjectID: p, PackageID: id, PackageVersion: row.Version, ContentHash: row.ContentHash, RecipientID: recipient, ChannelID: channel, Attempt: attempt, State: "FAILED", ActorID: authn.PrincipalIDFromContext(ctx), CreatedAt: now()}
	// A real local message and the send ledger commit together. No email/webhook
	// outcome or read receipt can be inferred from this transport.
	body := fmt.Sprintf("Delivery %s\n%s\nManifest SHA-256: %s", id.String(), row.Title, row.ContentHash)
	var message guid.ID
	if m.packageSender.SendOn == nil {
		err = ErrUnavailable
	} else {
		if err = tx.SavePoint("package_send").Error; err != nil {
			return Receipt{}, err
		}
		message, err = m.packageSender.SendOn(ctx, tx, row.TenantID, p, channel, recipient, body)
		if err != nil {
			if e := tx.RollbackTo("package_send").Error; e != nil {
				return Receipt{}, e
			}
		}
	}
	kind := "package_send_failed"
	if err == nil {
		if message <= 0 {
			return Receipt{}, ErrUnavailable
		}
		entry.State = "SUCCEEDED"
		entry.MessageID = &message
		kind = "package_sent"
	} else {
		if !errors.Is(err, ErrUnavailable) && !errors.Is(err, collab.ErrUnavailable) {
			return Receipt{}, err
		}
		failure := "CHANNEL_TRANSPORT_UNAVAILABLE"
		entry.FailureCode = &failure
	}
	if err = q.WithContext(ctx).Create(entry); err != nil {
		return Receipt{}, err
	}
	if err = m.packageSendEventOn(ctx, tx, entry, kind); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: id, Version: row.Version, State: entry.State}, nil
}
func (m *Module) packageSendEventOn(ctx context.Context, tx *gorm.DB, row *model.PackageSend, kind string) error {
	// Send entry identity/version prevents notifications from conflating approval
	// with transport; only frozen non-secret routing facts leave this boundary.
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	detail := map[string]any{"project_id": row.ProjectID.String(), "package_id": row.PackageID.String(), "package_version": row.PackageVersion, "content_hash": row.ContentHash, "recipient_id": row.RecipientID.String(), "channel_id": row.ChannelID.String(), "attempt_no": row.Attempt, "transport": "CLIENT_SHARED", "state": row.State, "message_id": row.MessageID, "failure_code": row.FailureCode, "authority_source": "permission:delivery_send"}
	event := audit.NewDomainEvent(ctx, row.TenantID, "clientwork."+kind, Name, row.ID)
	event.Detail = detail
	if err := m.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "clientwork." + kind, Subject: events.Subject{Kind: Name, ID: row.ID}, SubjectVersion: 1, Actor: events.Actor{Kind: "HUMAN", ID: row.ActorID, AuthoritySource: "permission:delivery_send"}, Data: detail})
	return err
}
func (m *Module) packageSendsHTTP(ctx context.Context, in *PackageTimelineInput) (*respx.Body[Page[PackageSendView]], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return readDomain(ctx, m, &in.ListInput, "package-sends:"+id.String(), "delivery_send", false, func(tx *gorm.DB, external bool, last guid.ID, n int) ([]PackageSendView, []guid.ID, error) {
		p, e := guid.RequireID(in.EntityID)
		if e != nil {
			return nil, nil, ErrInvalid
		}
		row, e := m.packageOn(ctx, tx, p, id, false)
		if e != nil {
			return nil, nil, e
		}
		q := query.BindPackageSend(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(p), q.PackageID.Eq(id), q.ID.Gt(last)).Order(q.ID).Limit(n).Find()
		if e != nil {
			return nil, nil, e
		}
		out := make([]PackageSendView, 0, len(rows))
		ids := make([]guid.ID, 0, len(rows))
		for _, r := range rows {
			out = append(out, PackageSendView{ID: r.ID, PackageVersion: r.PackageVersion, ContentHash: r.ContentHash, RecipientID: r.RecipientID, ChannelID: r.ChannelID, Attempt: r.Attempt, State: r.State, MessageID: r.MessageID, FailureCode: r.FailureCode, ActorID: r.ActorID, CreatedAt: r.CreatedAt})
			ids = append(ids, r.ID)
		}
		return out, ids, nil
	})
}
