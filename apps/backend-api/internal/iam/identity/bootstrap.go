package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BootstrapPrincipalInput describes the identity half of the platform
// administrator bootstrap: one active principal that belongs to no tenant.
type BootstrapPrincipalInput struct {
	LoginName   string
	DisplayName string
	Email       string
}

// BootstrapPrincipalStore is the narrow identity capability the composition
// boundary binds to its own bootstrap transaction. Find locks the login row so
// two concurrent bootstraps cannot both decide the login is free.
type BootstrapPrincipalStore struct {
	Find   func(context.Context, *gorm.DB, string) (Principal, bool, error)
	Create func(context.Context, *gorm.DB, BootstrapPrincipalInput, time.Time) (guid.ID, error)
}

// NormalizeBootstrapLogin applies the same login normalization the rest of
// identity uses. The bootstrap command needs it before the transaction opens
// so the value it reports matches the value it looked up.
func NormalizeBootstrapLogin(loginName string) string { return normalizeLogin(loginName) }

// FindPrincipalByLoginOn locks and returns the principal owning loginName. It
// reports false when the login is free. The lock is what makes the caller's
// "no administrator yet" check hold until commit.
func (r *Repository) FindPrincipalByLoginOn(
	ctx context.Context,
	db *gorm.DB,
	loginName string,
) (Principal, bool, error) {
	loginName = normalizeLogin(loginName)
	if r == nil || db == nil || loginName == "" {
		return Principal{}, false, ErrInvalid
	}
	repo, err := r.on(db)
	if err != nil {
		return Principal{}, false, err
	}
	tables, database, err := repo.tables()
	if err != nil {
		return Principal{}, false, err
	}
	query := database.WithContext(ctx).Table(tables.Principals.TableName()).Where("login_name = ?", loginName)
	// SQLite has no row lock to take; its writer serializes the transaction
	// already, so asking for one would only fail to parse.
	if database.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate})
	}
	var row model.Principal
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Principal{}, false, nil
		}
		return Principal{}, false, fmt.Errorf("lock principal by login: %w", err)
	}
	return principalFromRow(&row), true, nil
}

// CreateBootstrapPrincipalOn writes one active principal through the caller's
// transaction. It grants no tenant membership and no credential: the platform
// administrator is deliberately not a member of anything.
func (r *Repository) CreateBootstrapPrincipalOn(
	ctx context.Context,
	db *gorm.DB,
	input BootstrapPrincipalInput,
	now time.Time,
) (guid.ID, error) {
	input, err := normalizeBootstrapPrincipal(input)
	if err != nil {
		return 0, err
	}
	if r == nil || db == nil {
		return 0, ErrInvalid
	}
	repo, err := r.on(db)
	if err != nil {
		return 0, err
	}
	id, err := repo.nextID()
	if err != nil {
		return 0, err
	}
	now = identityTime(now)
	principal := model.Principal{
		ID: id, LoginName: input.LoginName, Email: input.Email,
		EmailVerified: input.Email != "", DisplayName: input.DisplayName,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.insertPrincipal(ctx, &principal); err != nil {
		return 0, fmt.Errorf("insert bootstrap principal: %w", err)
	}
	return id, nil
}

func normalizeBootstrapPrincipal(input BootstrapPrincipalInput) (BootstrapPrincipalInput, error) {
	input.LoginName = normalizeLogin(input.LoginName)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.LoginName == "" || len(input.LoginName) > 200 ||
		len(input.DisplayName) > 128 || len(input.Email) > 320 {
		return BootstrapPrincipalInput{}, ErrInvalid
	}
	if input.Email != "" {
		parsed, err := mail.ParseAddress(input.Email)
		if err != nil || !strings.EqualFold(parsed.Address, input.Email) {
			return BootstrapPrincipalInput{}, ErrInvalid
		}
	}
	if input.DisplayName == "" {
		input.DisplayName = input.LoginName
	}
	return input, nil
}
