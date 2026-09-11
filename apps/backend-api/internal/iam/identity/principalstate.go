package identity

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// PrincipalStateStore is the narrow principal-state capability the modules that
// authenticate a principal resolve, instead of naming the principal table
// themselves. Identity owns the table, so it owns the statements that read it.
//
// Every function takes the caller's database handle and runs there, which is
// the point of the shape: a caller already inside a transaction locks and reads
// the row in the same snapshot as the write it guards, so a suspension cannot
// land between the check and the write.
//
// The capability is bound to one deployment prefix when it is built, so a
// caller never carries a prefix and two callers cannot disagree about one.
type PrincipalStateStore struct {
	// Lock takes the principal's row lock without changing its state. The
	// write-back of the current value is how every supported engine is made to
	// take it.
	Lock func(context.Context, *gorm.DB, guid.ID) error
	// LockActive takes the row lock only while the principal is active, so a
	// suspended principal is left unlocked and the read that follows rejects it.
	LockActive func(context.Context, *gorm.DB, guid.ID) error
	// Status reads the principal's status. found is false when no principal
	// carries that id, so an absence is reported as an absence rather than as a
	// status or a failure.
	Status func(context.Context, *gorm.DB, guid.ID) (status string, found bool, err error)
}

// principalStateStore binds the statements to one deployment prefix. The prefix
// reaches the table name through the module's own BindNames, so it is applied
// the same way it is everywhere else identity queries.
func principalStateStore(prefix string) PrincipalStateStore {
	principalTable := query.BindNames(prefix).Principals.String()
	return PrincipalStateStore{
		Lock: func(ctx context.Context, db *gorm.DB, principalID guid.ID) error {
			return db.WithContext(ctx).Table(principalTable).Where("id = ?", principalID).
				Update("updated_at", gorm.Expr("updated_at")).Error
		},
		LockActive: func(ctx context.Context, db *gorm.DB, principalID guid.ID) error {
			return db.WithContext(ctx).Table(principalTable).Where("id = ? AND status = ?", principalID, "active").
				Update("updated_at", gorm.Expr("updated_at")).Error
		},
		Status: func(ctx context.Context, db *gorm.DB, principalID guid.ID) (string, bool, error) {
			var row struct{ Status string }
			err := db.WithContext(ctx).Table(principalTable).
				Select("status").Where("id = ?", principalID).Take(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", false, nil
			}
			if err != nil {
				return "", false, fmt.Errorf("load principal status: %w", err)
			}
			return row.Status, true, nil
		},
	}
}

// principalStateStore is provided before injection. It needs nothing but the
// deployment prefix, but an unwired module must still refuse rather than fall
// back to an unprefixed table.
func (m *Module) principalStateStore() PrincipalStateStore {
	if m == nil || m.runtime == nil {
		return unavailablePrincipalStateStore()
	}
	return principalStateStore(m.runtime.TablePrefix())
}

func unavailablePrincipalStateStore() PrincipalStateStore {
	return PrincipalStateStore{
		Lock:       func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable },
		LockActive: func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable },
		Status: func(context.Context, *gorm.DB, guid.ID) (string, bool, error) {
			return "", false, ErrUnavailable
		},
	}
}
