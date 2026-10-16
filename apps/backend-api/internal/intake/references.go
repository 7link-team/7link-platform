package intake

import (
	"context"
	"database/sql/driver"
	"errors"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"gorm.io/gorm"
)

var (
	ErrUnavailable     = errors.New("intake: reference reader unavailable")
	ErrInvalidArgument = errors.New("intake: invalid reference request")
)

// IntakeReader supplies transactional record and contact reference facts.
// Channel, clarification and aggregate readers remain to be implemented.
type IntakeReader struct {
	GetOn      func(context.Context, *gorm.DB, guid.ID, guid.ID) (IntakeRef, bool, error)
	ContactsOn func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]ContactReference, error)
}

// ContactReference exposes ownership and lifecycle facts without private contact
// details. It does not grant project membership or external access.
type ContactReference struct {
	ID, TenantID guid.ID
	PrincipalID  *guid.ID
	Kind, State  string
	ArchivedAt   *time.Time
}

// ContactsOn returns each existing tenant-owned ID once, ordered by ID. Missing
// and foreign IDs are absent. Blocked/archived records retain their state so the
// consuming command can apply its operation-specific policy. An empty request
// never becomes an unfiltered contact list.
func (m *Module) ContactsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, ids []guid.ID) ([]ContactReference, error) {
	if m == nil || m.names.Contacts.IsZero() {
		return nil, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]ContactReference, 0, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	unique := make([]driver.Valuer, 0, len(ids))
	seen := make(map[guid.ID]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrInvalidArgument
		}
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	q := query.BindContacts(tx, m.names)
	rows, err := q.WithContext(ctx).Select(q.ID, q.TenantID, q.PrincipalID, q.Kind, q.State, q.ArchivedAt).Where(q.TenantID.Eq(tenant), q.ID.In(unique...)).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result = append(result, ContactReference{ID: row.ID, TenantID: row.TenantID, PrincipalID: row.PrincipalID, Kind: row.Kind, State: row.State, ArchivedAt: row.ArchivedAt})
	}
	return result, nil
}

// IntakeRef identifies a source record without exposing its original content,
// internal decline reason, reply token or client-approval details.
type IntakeRef struct {
	ID, TenantID, ContactID, ChannelID           guid.ID
	RefNo                                        int
	Title, Kind, ChannelKind, Status             string
	ProjectID, RequirementID, OriginAcceptanceID *guid.ID
}

// GetOn reads a tenant-scoped source reference in the caller transaction.
func (m *Module) GetOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (IntakeRef, bool, error) {
	if m == nil || m.names.Records.IsZero() {
		return IntakeRef{}, false, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return IntakeRef{}, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return IntakeRef{}, false, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Select(q.ID, q.TenantID, q.RefNo, q.Title, q.Kind, q.ContactID, q.ChannelID, q.ChannelKind, q.Status, q.ProjectID, q.RequirementID, q.OriginAcceptanceID).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return IntakeRef{}, false, nil
	}
	if err != nil {
		return IntakeRef{}, false, err
	}
	return IntakeRef{ID: row.ID, TenantID: row.TenantID, RefNo: row.RefNo, Title: row.Title, Kind: row.Kind, ContactID: row.ContactID, ChannelID: row.ChannelID, ChannelKind: row.ChannelKind, Status: row.Status, ProjectID: row.ProjectID, RequirementID: row.RequirementID, OriginAcceptanceID: row.OriginAcceptanceID}, true, nil
}
