package notification

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"gorm.io/gorm"
)

const PrincipalErasureVersion int64 = 4

// ErasePrincipalData removes recipient-owned inbox, preference and delivery
// data under the caller's principal lifecycle lock and transaction. A queued
// delivery whose record has been erased cannot send its former sealed payload.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, principal guid.ID, _ time.Time) error {
	if !validTransaction(tx) || principal <= 0 {
		return ErrInvalid
	}
	names := query.BindNames(prefix)
	s := query.BindSourceReceipts(tx, names)
	if _, err := s.WithContext(ctx).Where(s.RecipientPrincipalID.Eq(principal)).Delete(); err != nil {
		return err
	}
	d := query.BindDeliveries(tx, names)
	if _, err := d.WithContext(ctx).Where(d.RecipientPrincipalID.Eq(principal)).Delete(); err != nil {
		return err
	}
	i := query.BindIntakeReceipts(tx, names)
	if _, err := i.WithContext(ctx).Where(i.RecipientPrincipalID.Eq(principal)).Delete(); err != nil {
		return err
	}
	n := query.BindNotifications(tx, names)
	if _, err := n.WithContext(ctx).Where(n.RecipientPrincipalID.Eq(principal)).Delete(); err != nil {
		return err
	}
	p := query.BindPreferences(tx, names)
	_, err := p.WithContext(ctx).Where(p.PrincipalID.Eq(principal)).Delete()
	return err
}
