package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
)

var (
	ErrContactNotFound = errors.New("delivery: contact reference not found")
	ErrIntakeNotFound  = errors.New("delivery: source intake not found")
)

// projectReferencesOn validates source ownership after command authorization.
// It neither changes the source record nor grants a contact access to a project.
// Contact lifecycle restrictions on sending/accepting remain with those actions.
func (m *Module) projectReferencesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, input projectCreateBody) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m == nil || m.sources.ContactsOn == nil || m.sources.GetOn == nil {
		return ErrUnavailable
	}
	if input.OriginIntakeID != nil {
		if *input.OriginIntakeID <= 0 {
			return ErrInvalidArgument
		}
		ref, found, err := m.sources.GetOn(ctx, tx, tenant, *input.OriginIntakeID)
		if err != nil {
			return err
		}
		if !found {
			return ErrIntakeNotFound
		}
		if ref.ID != *input.OriginIntakeID || ref.TenantID != tenant {
			return ErrUnavailable
		}
	}
	ids := make([]guid.ID, 0, len(input.Contacts))
	seen := make(map[guid.ID]bool, len(input.Contacts))
	for _, contact := range input.Contacts {
		if contact.IntakeContactID <= 0 || seen[contact.IntakeContactID] {
			return ErrInvalidArgument
		}
		seen[contact.IntakeContactID] = true
		ids = append(ids, contact.IntakeContactID)
	}
	if len(ids) == 0 {
		return nil
	}
	refs, err := m.sources.ContactsOn(ctx, tx, tenant, ids)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.TenantID != tenant || !seen[ref.ID] {
			return ErrUnavailable
		}
		delete(seen, ref.ID)
	}
	if len(seen) > 0 {
		return ErrContactNotFound
	}
	return nil
}
