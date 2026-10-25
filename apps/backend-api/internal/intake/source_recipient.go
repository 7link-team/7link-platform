package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"context"
	"gorm.io/gorm"
)

// The source contact, rather than any client in the project, determines the
// recipient of original-thread communication. No email identity is inferred.
func (m *Module) sourceRecipientOn(ctx context.Context, tx *gorm.DB, tenant, project, source, recipient guid.ID) error {
	if recipient <= 0 {
		return ErrInvalidArgument
	}
	if err := m.dispositionSourceReadAuthorityOn(ctx, tx, tenant, project, source); err != nil {
		return err
	}
	r := query.BindRecords(tx, m.names)
	row, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ID.Eq(source)).First()
	if err != nil {
		return err
	}
	c := query.BindContacts(tx, m.names)
	_, err = c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(row.ContactID), c.PrincipalID.Eq(recipient), c.State.Eq("ACTIVE"), c.ArchivedAt.IsNull()).First()
	return err
}
