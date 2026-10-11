package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	// ActiveRow reads a whole active principal. found is false when no active
	// principal carries that id.
	ActiveRow func(context.Context, *gorm.DB, guid.ID) (principal Principal, found bool, err error)
	// LockActiveRow takes the row lock and reads the principal in one statement,
	// and reports whether there was an active row to lock. That report is the
	// point: a caller that keeps only the error relies on an absent or suspended
	// principal stopping the operation, so a lock that cannot say whether it
	// locked anything would let a suspended principal through.
	LockActiveRow func(context.Context, *gorm.DB, guid.ID) (principal Principal, found bool, err error)
	// LockByLogin does the same by login name and without the active predicate,
	// for a caller that has to reconcile whatever state the login is in.
	LockByLogin func(context.Context, *gorm.DB, string) (principal Principal, found bool, err error)
	// HumanByLogin resolves a login name to a principal that is not a service
	// account. Identity owns the service-account table too, so the exclusion
	// belongs here rather than in the caller.
	HumanByLogin func(context.Context, *gorm.DB, string) (principal Principal, found bool, err error)
	// ActiveHumanByIdentifier resolves a login name or an email to the active,
	// email-verified people carrying it, at most two. It returns them rather
	// than one, because what an ambiguous identifier means is the caller's
	// policy, not identity's.
	ActiveHumanByIdentifier func(context.Context, *gorm.DB, string) ([]Principal, error)
	// VerifyEmail marks an active principal's pending email verified and clears
	// its activation requirement, reporting whether a row was in that state.
	VerifyEmail func(ctx context.Context, db *gorm.DB, id guid.ID, email string, now time.Time) (bool, error)

	// ActiveHumanByPhone resolves a proven phone number to the active people
	// carrying it, at most two. Numbers are not unique here -- a carrier reissues
	// them -- so what more than one means is the caller's policy, as it is for an
	// identifier.
	ActiveHumanByPhone func(context.Context, *gorm.DB, string) ([]Principal, error)

	// VerifyPhone marks an active principal's number proven. The number is part
	// of the predicate: a code proves the number it was sent to, so one that
	// changed in between proves nothing about the number held now.
	VerifyPhone func(ctx context.Context, db *gorm.DB, id guid.ID, phone string, now time.Time) (bool, error)
	// SelfRegistered reports whether public self-registration created the
	// principal. Only such a principal, while still pending, may have its
	// credential replaced by a later public registration; one a directory,
	// an invitation or an administrator created never is.
	SelfRegistered func(ctx context.Context, db *gorm.DB, id guid.ID) (bool, error)
}

// principalStateStore binds the statements to one deployment prefix. The prefix
// reaches the table name through the module's own BindNames, so it is applied
// the same way it is everywhere else identity queries.
func principalStateStore(prefix string) PrincipalStateStore {
	names := query.BindNames(prefix)
	principalTable := names.Principals.String()
	serviceAccountTable := names.ServiceAccounts.String()
	// The aliased forms the login readers join on, built once. They are the same
	// strings the statements used before, assembled in one place rather than at
	// every call.
	principalAliased := principalTable + " AS principal"
	serviceAccountAliased := serviceAccountTable + " AS service_account"
	// takeOne runs a single-row read and turns "no rows" into found=false, so
	// every reader below reports an absence the same way.
	takeOne := func(statement *gorm.DB) (Principal, bool, error) {
		var row Principal
		err := statement.Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Principal{}, false, nil
		}
		if err != nil {
			return Principal{}, false, fmt.Errorf("load principal: %w", err)
		}
		return row, true, nil
	}
	// notAServiceAccount is the exclusion both login readers apply. It is built
	// per call because it carries the caller's handle.
	notAServiceAccount := func(db *gorm.DB) *gorm.DB {
		return db.Table(serviceAccountAliased).
			Select("1").Where("service_account.principal_id = principal.id")
	}
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
		ActiveRow: func(ctx context.Context, db *gorm.DB, principalID guid.ID) (Principal, bool, error) {
			return takeOne(db.WithContext(ctx).Table(principalTable).
				Where("id = ? AND status = ?", principalID, "active"))
		},
		LockActiveRow: func(ctx context.Context, db *gorm.DB, principalID guid.ID) (Principal, bool, error) {
			return takeOne(forUpdate(db.WithContext(ctx).Table(principalTable).
				Where("id = ? AND status = ?", principalID, "active")))
		},
		LockByLogin: func(ctx context.Context, db *gorm.DB, loginName string) (Principal, bool, error) {
			return takeOne(forUpdate(db.WithContext(ctx).Table(principalTable).
				Where("login_name = ?", loginName)))
		},
		HumanByLogin: func(ctx context.Context, db *gorm.DB, loginName string) (Principal, bool, error) {
			return takeOne(db.WithContext(ctx).Table(principalAliased).
				Where("principal.login_name = ?", loginName).
				Where("NOT EXISTS (?)", notAServiceAccount(db)))
		},
		ActiveHumanByIdentifier: func(ctx context.Context, db *gorm.DB, identifier string) ([]Principal, error) {
			identifier = strings.ToLower(strings.TrimSpace(identifier))
			rows := make([]Principal, 0, 2)
			err := db.WithContext(ctx).Table(principalAliased).
				Where("status = ? AND email_verified = ?", "active", true).
				Where("LOWER(principal.login_name) = ? OR LOWER(principal.email) = ?", identifier, identifier).
				Where("NOT EXISTS (?)", notAServiceAccount(db)).
				Limit(2).Find(&rows).Error
			if err != nil {
				return nil, fmt.Errorf("load principals by identifier: %w", err)
			}
			return rows, nil
		},
		VerifyEmail: func(
			ctx context.Context, db *gorm.DB, principalID guid.ID, email string, now time.Time,
		) (bool, error) {
			result := db.WithContext(ctx).Table(principalTable).
				Where("id = ? AND status = ? AND email = ? AND email_verified = ?",
					principalID, "active", email, false).
				Updates(map[string]any{"email_verified": true, "activation_required": false, "updated_at": now})
			if result.Error != nil {
				return false, fmt.Errorf("verify principal email: %w", result.Error)
			}
			return result.RowsAffected == 1, nil
		},
		ActiveHumanByPhone: func(ctx context.Context, db *gorm.DB, phone string) ([]Principal, error) {
			phone = strings.TrimSpace(phone)
			rows := make([]Principal, 0, 2)
			if phone == "" {
				return rows, nil
			}
			// Only a proven number resolves: an unproven one is a string somebody
			// typed into their own profile, and anybody could type any number.
			err := db.WithContext(ctx).Table(principalAliased).
				Where("status = ? AND phone_verified = ? AND phone = ?", "active", true, phone).
				Where("NOT EXISTS (?)", notAServiceAccount(db)).
				Limit(2).Find(&rows).Error
			if err != nil {
				return nil, fmt.Errorf("load principals by phone: %w", err)
			}
			return rows, nil
		},
		VerifyPhone: func(
			ctx context.Context, db *gorm.DB, principalID guid.ID, phone string, now time.Time,
		) (bool, error) {
			result := db.WithContext(ctx).Table(principalTable).
				Where("id = ? AND status = ? AND phone = ? AND phone_verified = ?",
					principalID, "active", phone, false).
				Updates(map[string]any{"phone_verified": true, "updated_at": now})
			if result.Error != nil {
				return false, fmt.Errorf("verify principal phone: %w", result.Error)
			}
			return result.RowsAffected == 1, nil
		},
		SelfRegistered: func(ctx context.Context, db *gorm.DB, principalID guid.ID) (bool, error) {
			var row struct{ SelfRegistered bool }
			err := db.WithContext(ctx).Table(principalTable).
				Select("self_registered").Where("id = ?", principalID).Take(&row).Error
			if err != nil {
				return false, fmt.Errorf("load principal registration origin: %w", err)
			}
			return row.SelfRegistered, nil
		},
	}
}

// forUpdate adds the row lock every engine but SQLite takes, and SQLite is where
// the fixtures run. It is a function so the two lock readers cannot drift apart.
func forUpdate(statement *gorm.DB) *gorm.DB {
	if statement.Dialector != nil && statement.Dialector.Name() == "sqlite" {
		return statement
	}
	return statement.Clauses(clause.Locking{Strength: "UPDATE"})
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
		ActiveRow: func(context.Context, *gorm.DB, guid.ID) (Principal, bool, error) {
			return Principal{}, false, ErrUnavailable
		},
		LockActiveRow: func(context.Context, *gorm.DB, guid.ID) (Principal, bool, error) {
			return Principal{}, false, ErrUnavailable
		},
		LockByLogin: func(context.Context, *gorm.DB, string) (Principal, bool, error) {
			return Principal{}, false, ErrUnavailable
		},
		HumanByLogin: func(context.Context, *gorm.DB, string) (Principal, bool, error) {
			return Principal{}, false, ErrUnavailable
		},
		ActiveHumanByIdentifier: func(context.Context, *gorm.DB, string) ([]Principal, error) {
			return nil, ErrUnavailable
		},
		VerifyEmail: func(context.Context, *gorm.DB, guid.ID, string, time.Time) (bool, error) {
			return false, ErrUnavailable
		},
		ActiveHumanByPhone: func(context.Context, *gorm.DB, string) ([]Principal, error) {
			return nil, ErrUnavailable
		},
		VerifyPhone: func(context.Context, *gorm.DB, guid.ID, string, time.Time) (bool, error) {
			return false, ErrUnavailable
		},
		SelfRegistered: func(context.Context, *gorm.DB, guid.ID) (bool, error) {
			return false, ErrUnavailable
		},
	}
}
