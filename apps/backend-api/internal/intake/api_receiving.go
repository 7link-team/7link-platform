package intake

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// APIReceiving creates genuine immutable intake sources. The caller holds the
// current project fence and channel policy lock, and checks its credential.
type APIReceiving struct {
	ReceiveOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, APIReceiptInput) (IntakeLedgerEntry, error)
}
type APIReceiptInput struct {
	ContactID guid.ID `json:"contact_id,omitempty" doc:"Optional active tenant contact; omitted resolves the current API caller SYSTEM contact."`
	Title     string  `json:"title" maxLength:"200"`
	Content   string  `json:"content" maxLength:"65536"`
	Format    string  `json:"format" enum:"PLAIN,MARKDOWN"`
}

func (m *Module) provideApiReceiving(s *module.Services) error {
	return module.Provide[APIReceiving](s, APIReceiving{ReceiveOn: m.receiveAPIOn})
}
func (m *Module) receiveAPIOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, in APIReceiptInput) (IntakeLedgerEntry, error) {
	var out IntakeLedgerEntry
	if channel <= 0 || in.ContactID < 0 || !utf8.ValidString(in.Title) || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 200 || !utf8.ValidString(in.Content) || strings.TrimSpace(in.Content) == "" || len(in.Content) > 65536 || (in.Format != "PLAIN" && in.Format != "MARKDOWN") {
		return out, ErrInvalidArgument
	}
	if err := m.changeSourceAuthority(ctx, tx, tenant, project, "intake_submit"); err != nil {
		return out, err
	}
	if m.brief.ids == nil || m.brief.audit == nil || m.brief.events == nil {
		return out, ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	c := query.BindContacts(tx, m.names)
	if in.ContactID == 0 {
		// changeSourceAuthority holds the caller's principal lock until commit,
		// serializing first submissions across projects for this tenant/caller.
		contact, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.PrincipalID.Eq(actor)).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			id, err := m.brief.ids.Next()
			if err != nil {
				return out, err
			}
			if id <= 0 {
				return out, ErrUnavailable
			}
			at := time.Now().UTC().Truncate(time.Millisecond)
			contact = &model.Contact{ID: guid.ID(id), TenantID: tenant, Kind: "SYSTEM", DisplayName: "API caller", PrincipalID: &actor, State: "ACTIVE", Version: 1, CreatedAt: at, UpdatedAt: at, CreatedBy: &actor, UpdatedBy: &actor}
			if err := c.WithContext(ctx).Create(contact); err != nil {
				return out, err
			}
		} else if err != nil {
			return out, err
		}
		if contact.Kind != "SYSTEM" || contact.State != "ACTIVE" || contact.ArchivedAt != nil {
			return out, ErrInvalidArgument
		}
		in.ContactID = contact.ID
	}
	_, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.ID.Eq(in.ContactID), c.State.Eq("ACTIVE"), c.ArchivedAt.IsNull()).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrInvalidArgument
	}
	if err != nil {
		return out, err
	}
	// Current IAM authorization pins the tenant policy revision until commit.
	// Locking reads avoid a prior repeatable-read snapshot when allocating refs.
	q := query.BindRecords(tx, m.names)
	last, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant)).Order(q.RefNo.Desc()).Take()
	ref := 1
	if err == nil {
		ref = last.RefNo + 1
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	if ref <= 0 || ref > 2147483647 {
		return out, ErrConflict
	}
	id, err := m.brief.ids.Next()
	if err != nil {
		return out, err
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	var token [8]byte
	if _, err = rand.Read(token[:]); err != nil {
		return out, err
	}
	hash := sha256.Sum256([]byte(in.Content))
	at := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.Record{ID: guid.ID(id), TenantID: tenant, ProjectID: &project, ChannelID: channel, ChannelKind: "API", ContactID: in.ContactID, Kind: "NEW", Status: "RECEIVED", Title: in.Title, RawContent: &in.Content, RawContentFormat: in.Format, RawContentHash: hex.EncodeToString(hash[:]), RefNo: ref, ReplyToken: hex.EncodeToString(token[:]), ReceivedAt: at, SubmittedByPrincipalID: &actor, Version: 1, CreatedAt: at, UpdatedAt: at, CreatedBy: &actor, UpdatedBy: &actor}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return out, err
	}
	data := map[string]any{"source": "AUTHENTICATED_API", "channel_id": channel.String(), "content_hash": row.RawContentHash}
	event := audit.NewDomainEvent(ctx, tenant, "intake.received", "intake", row.ID)
	event.Detail = data
	if err = m.brief.audit(ctx, tx, event); err != nil {
		return out, err
	}
	_, err = m.brief.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "intake.received", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:intake_submit"}, Subject: events.Subject{Kind: "intake", ID: row.ID}, SubjectVersion: 1, Data: data})
	return intakeLedgerEntry(row, project), err
}
