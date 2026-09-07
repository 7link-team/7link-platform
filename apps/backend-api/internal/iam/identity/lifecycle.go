package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrLifecycleConflict   = errors.New("principal lifecycle version conflict")
	ErrPrincipalRetained   = errors.New("principal is subject to retention")
	ErrPrincipalAnonymized = errors.New("principal is permanently anonymized")
	ErrSuspensionRequired  = errors.New("principal must be suspended before anonymization")
)

// PrincipalAnonymizer composes operational PII owners in the caller's transaction.
// It must fail unless every installed owner's records can be erased safely,
// including data belonging to modules that are currently disabled.
type PrincipalAnonymizer func(context.Context, *gorm.DB, Principal, time.Time) error

// PrincipalLifecycle is global platform state, independent of tenant membership.
// Version zero means no retention decision has been recorded; erasure is denied.
type PrincipalLifecycle struct {
	PrincipalID  guid.ID    `json:"principal_id"`
	Version      int64      `json:"version"`
	RetainUntil  *time.Time `json:"retain_until"`
	LegalHold    bool       `json:"legal_hold"`
	Reference    string     `json:"reference"`
	AnonymizedAt *time.Time `json:"anonymized_at"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

// GetPrincipalLifecycle returns retention state without creating a policy.
func (s *Service) GetPrincipalLifecycle(ctx context.Context, id guid.ID) (PrincipalLifecycle, error) {
	if id <= 0 {
		return PrincipalLifecycle{}, ErrInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return PrincipalLifecycle{}, err
	}
	if _, err := repo.lifecyclePrincipal(ctx, id, false); err != nil {
		return PrincipalLifecycle{}, err
	}
	row, err := repo.principalLifecycle(ctx, id)
	return lifecycleView(id, row), err
}

// SetPrincipalRetention records an explicit deadline and optional legal hold.
// Reference is an administrative case identifier, not a copy of the subject's PII.
func (s *Service) SetPrincipalRetention(
	ctx context.Context, id guid.ID, version int64, until time.Time, hold bool, reference string,
) (PrincipalLifecycle, error) {
	reference = strings.TrimSpace(reference)
	if id <= 0 || version < 0 || until.IsZero() || until.Year() < 1970 || until.Year() > 9999 ||
		reference == "" || len(reference) > 200 {
		return PrincipalLifecycle{}, ErrInvalid
	}
	var result PrincipalLifecycle
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.lifecyclePrincipal(ctx, id, true); err != nil {
			return err
		}
		current, err := repo.principalLifecycle(ctx, id)
		if err != nil {
			return err
		}
		if current.AnonymizedAt != nil {
			return ErrPrincipalAnonymized
		}
		if current.Version != version {
			return ErrLifecycleConflict
		}
		now, err := lifecycleNow(ctx, repo.writer())
		if err != nil {
			return err
		}
		row := model.PrincipalLifecycle{
			PrincipalID: id, Version: version + 1, RetainUntil: identityTime(until),
			LegalHold: hold, Reference: reference, UpdatedAt: now,
		}
		if err := repo.savePrincipalLifecycle(ctx, &row, version); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, 0, "principal_retention_updated", "principal", id)
		event.Detail["version"], event.Detail["legal_hold"] = row.Version, row.LegalHold
		event.Detail["retain_until"] = row.RetainUntil
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		result = lifecycleView(id, row)
		return nil
	})
	return result, err
}

// AnonymizePrincipal permanently erases a suspended principal's operational PII.
// The linked cleanup, tombstone and append-only audit entry commit together.
func (s *Service) AnonymizePrincipal(ctx context.Context, id guid.ID, version int64) (PrincipalLifecycle, error) {
	if id <= 0 || version < 1 {
		return PrincipalLifecycle{}, ErrInvalid
	}
	if s.anonymizer == nil {
		return PrincipalLifecycle{}, ErrUnavailable
	}
	var result PrincipalLifecycle
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		principal, err := repo.lifecyclePrincipal(ctx, id, true)
		if err != nil {
			return err
		}
		row, err := repo.principalLifecycle(ctx, id)
		if err != nil {
			return err
		}
		if row.Version != version {
			return ErrLifecycleConflict
		}
		if row.AnonymizedAt != nil {
			result = lifecycleView(id, row)
			return nil
		}
		if principal.Status != "disabled" {
			return ErrSuspensionRequired
		}
		now, err := lifecycleNow(ctx, repo.writer())
		if err != nil {
			return err
		}
		if row.LegalHold || row.RetainUntil.IsZero() || row.RetainUntil.After(now) {
			return ErrPrincipalRetained
		}
		if err := s.anonymizer(ctx, repo.writer(), principalFromRow(principal), now); err != nil {
			return fmt.Errorf("erase linked principal data: %w", err)
		}
		if err := repo.updatePrincipalRow(ctx, id, map[string]any{
			"login_name": "erased:" + id.String(), "display_name": "Anonymized account", "email": "",
			"email_verified": false, "activation_required": false, "updated_at": now,
		}); err != nil {
			return err
		}
		row.Version++
		row.AnonymizedAt, row.UpdatedAt, row.Reference = &now, now, ""
		if err := repo.savePrincipalLifecycle(ctx, &row, version); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, 0, "principal_anonymized", "principal", id)
		event.Detail["version"] = row.Version
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		result = lifecycleView(id, row)
		return nil
	})
	return result, err
}

func (r *Repository) lifecyclePrincipal(ctx context.Context, id guid.ID, lock bool) (*model.Principal, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	query := db.WithContext(ctx).Table(tables.Principals.TableName()).Where("id = ?", id)
	if lock && db.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var row model.Principal
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var count int64
	if err := db.WithContext(ctx).Table(tables.ServiceAccounts.TableName()).Where("principal_id = ?", id).Count(&count).Error; err != nil {
		return nil, err
	}
	if count != 0 {
		return nil, ErrNotFound
	}
	return &row, nil
}

func (r *Repository) principalLifecycle(ctx context.Context, id guid.ID) (model.PrincipalLifecycle, error) {
	var row model.PrincipalLifecycle
	query := r.writer().WithContext(ctx).Table(r.table("iam_principal_lifecycles")).Where("principal_id = ?", id)
	// Lifecycle writes already hold the parent lock. A current read also avoids a
	// stale policy snapshot under MySQL REPEATABLE READ after waiting for that lock.
	if r.inTx && r.writer().Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.PrincipalLifecycle{}, nil
	}
	return row, err
}

func (r *Repository) savePrincipalLifecycle(ctx context.Context, row *model.PrincipalLifecycle, version int64) error {
	query := r.writer().WithContext(ctx).Table(r.table("iam_principal_lifecycles"))
	if version == 0 {
		result := query.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLifecycleConflict
		}
		return nil
	}
	result := query.Where("principal_id = ? AND version = ? AND anonymized_at IS NULL", row.PrincipalID, version).
		Updates(map[string]any{
			"version": row.Version, "retain_until": row.RetainUntil, "legal_hold": row.LegalHold,
			"reference": row.Reference, "anonymized_at": row.AnonymizedAt, "updated_at": row.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLifecycleConflict
	}
	return nil
}

func lifecycleNow(ctx context.Context, db *gorm.DB) (time.Time, error) {
	var millis int64
	err := db.WithContext(ctx).Raw("SELECT " + gormx.NowMillisExpr(db.Dialector.Name())).Scan(&millis).Error
	return time.UnixMilli(millis).UTC(), err
}

func lifecycleView(id guid.ID, row model.PrincipalLifecycle) PrincipalLifecycle {
	view := PrincipalLifecycle{PrincipalID: id, Version: row.Version, LegalHold: row.LegalHold,
		Reference: row.Reference, AnonymizedAt: row.AnonymizedAt}
	if row.Version > 0 {
		view.RetainUntil, view.UpdatedAt = &row.RetainUntil, &row.UpdatedAt
	}
	return view
}
