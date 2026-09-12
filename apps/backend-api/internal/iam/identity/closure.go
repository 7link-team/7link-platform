package identity

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var ErrClosureConflict = errors.New("closure request version or state conflict")

// PrincipalCredentialsRevoker invalidates all local credentials in the caller's
// principal-locked transaction, including the credential version and PATs.
type PrincipalCredentialsRevoker func(context.Context, *gorm.DB, guid.ID) error

// PrincipalCredentialStore is how identity asks the module that owns the
// credential tables to write them. identity creates a principal's first
// password, invalidates tokens when a login or an email changes, and revokes
// security state when a principal is suspended -- all of which land in tables
// authn owns, so authn runs the statements and identity sequences them.
//
// Every function takes the caller's handle and runs there, so the writes stay
// inside the transaction identity opened: a failure after one of them has to
// roll it back with everything else.
type PrincipalCredentialStore struct {
	// CreateHashed stores a principal's first password, already hashed by the
	// caller -- identity has the hash in hand at registration and invitation
	// time, and passing it on avoids hashing the same password twice.
	CreateHashed func(ctx context.Context, tx *gorm.DB, id guid.ID, hash string, now time.Time) error
	// IncrementVersion invalidates every token issued against the principal's
	// current credential. found is false when the principal has no credential at
	// all, which is a state identity distinguishes: a federation-only principal
	// legitimately has none.
	IncrementVersion func(ctx context.Context, tx *gorm.DB, id guid.ID, now time.Time) (found bool, err error)
	// RevokeSessions ends every unrevoked session the principal holds.
	RevokeSessions func(ctx context.Context, tx *gorm.DB, id guid.ID, now time.Time) error
	// ConsumeCredentialTokens spends the principal's outstanding password
	// recovery and email verification tokens, so a suspension cannot be undone
	// by a link issued before it.
	ConsumeCredentialTokens func(ctx context.Context, tx *gorm.DB, id guid.ID, now time.Time) error
}

// PrincipalOAuthRevoker withdraws every application grant in that transaction.
type PrincipalOAuthRevoker func(context.Context, *gorm.DB, guid.ID) error

// ClosureRequest exposes only workflow metadata. Erasure completion is derived
// from the existing lifecycle tombstone, never asserted by an applicant.
type ClosureRequest struct {
	PrincipalID guid.ID    `json:"principal_id"`
	Status      string     `json:"status"`
	Version     int64      `json:"version"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

func (s *Service) GetClosureRequest(ctx context.Context, principalID guid.ID) (ClosureRequest, error) {
	if principalID <= 0 {
		return ClosureRequest{}, ErrInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return ClosureRequest{}, err
	}
	if _, err := repo.lifecyclePrincipal(ctx, principalID, false); err != nil {
		return ClosureRequest{}, err
	}
	return repo.closureRequest(ctx, principalID)
}

func (repo *Repository) closureRequest(ctx context.Context, principalID guid.ID) (ClosureRequest, error) {
	tables, _, err := repo.tables()
	if err != nil {
		return ClosureRequest{}, err
	}
	result := ClosureRequest{PrincipalID: principalID, Status: "none"}
	row, err := tables.ClosureRequests.WithContext(ctx).Where(tables.ClosureRequests.PrincipalID.Eq(principalID)).Take()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if row != nil {
		result.Status, result.Version, result.RequestedAt, result.UpdatedAt = row.Status, row.Version, &row.RequestedAt, &row.UpdatedAt
	}
	lifecycle, err := repo.principalLifecycle(ctx, principalID)
	if err != nil {
		return result, err
	}
	if lifecycle.AnonymizedAt != nil {
		result.Status = "completed"
	}
	return result, nil
}

// ChangeClosureRequest changes only the request. It never suspends the account,
// establishes retention, or bypasses the existing erasure prerequisites.
func (s *Service) ChangeClosureRequest(ctx context.Context, principalID guid.ID, version int64, status, cookie, proof string) (ClosureRequest, error) {
	if principalID <= 0 || version < 0 || (status != "pending" && status != "canceled" && status != "rejected") {
		return ClosureRequest{}, ErrInvalid
	}
	if status == "pending" && s.reauthenticate == nil {
		return ClosureRequest{}, ErrUnavailable
	}
	var result ClosureRequest
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		if status == "pending" {
			if err := s.reauthenticate(ctx, repo.writer(), principalID, cookie, proof, "principal_close", principalID.String()); err != nil {
				return err
			}
		}
		principal, err := repo.lifecyclePrincipal(ctx, principalID, true)
		if err != nil {
			return err
		}
		current, err := repo.closureRequest(ctx, principalID)
		if err != nil {
			return err
		}
		if current.Status == "completed" {
			return ErrPrincipalAnonymized
		}
		if status != "rejected" && principal.Status != "active" {
			return ErrPrincipalInactive
		}
		if current.Version != version || (status == "pending" && current.Status == "pending") || (status != "pending" && current.Status != "pending") {
			return ErrClosureConflict
		}
		now, err := lifecycleNow(ctx, repo.writer())
		if err != nil {
			return err
		}
		requestedAt := now
		if status != "pending" && current.RequestedAt != nil {
			requestedAt = *current.RequestedAt
		}
		row := model.ClosureRequest{PrincipalID: principalID, Status: status, Version: version + 1, RequestedAt: requestedAt, UpdatedAt: now}
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		if version == 0 {
			if err := tables.ClosureRequests.WithContext(ctx).Create(&row); err != nil {
				return err
			}
		} else {
			update, err := tables.ClosureRequests.WithContext(ctx).Where(tables.ClosureRequests.PrincipalID.Eq(principalID), tables.ClosureRequests.Version.Eq(version)).
				Updates(map[string]any{"status": status, "version": version + 1, "requested_at": requestedAt, "updated_at": now})
			if err != nil {
				return err
			}
			if update.RowsAffected != 1 {
				return ErrClosureConflict
			}
		}
		event := audit.NewDomainEvent(ctx, 0, "principal_closure_request_changed", "principal", principalID)
		event.Detail["status"], event.Detail["version"] = status, row.Version
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		result = ClosureRequest{PrincipalID: principalID, Status: status, Version: row.Version, RequestedAt: &requestedAt, UpdatedAt: &now}
		return nil
	})
	return result, err
}

func (s *Service) ListClosureRequests(ctx context.Context, status string, offset, limit int) ([]ClosureRequest, int64, error) {
	if offset < 0 || limit < 1 || limit > 200 || (status != "" && status != "pending" && status != "processing" && status != "canceled" && status != "rejected" && status != "completed") {
		return nil, 0, ErrInvalid
	}
	tables, _, err := s.repo.tables()
	if err != nil {
		return nil, 0, err
	}
	requests, lifecycles := tables.ClosureRequests, tables.PrincipalLifecycles
	query := requests.WithContext(ctx).LeftJoin(lifecycles, lifecycles.PrincipalID.EqCol(requests.PrincipalID))
	if status == "completed" {
		query = query.Where(lifecycles.AnonymizedAt.IsNotNull())
	} else if status != "" {
		query = query.Where(lifecycles.AnonymizedAt.IsNull(), requests.Status.Eq(status))
	}
	total, err := query.Count()
	if err != nil {
		return nil, 0, err
	}
	items := []ClosureRequest{}
	var rows []struct {
		model.ClosureRequest
		AnonymizedAt *time.Time
	}
	err = query.Select(requests.ALL, lifecycles.AnonymizedAt).Order(requests.UpdatedAt, requests.PrincipalID).Offset(offset).Limit(limit).Scan(&rows)
	for _, row := range rows {
		item := ClosureRequest{PrincipalID: row.PrincipalID, Status: row.Status, Version: row.Version, RequestedAt: &row.RequestedAt, UpdatedAt: &row.UpdatedAt}
		if row.AnonymizedAt != nil {
			item.Status = "completed"
		}
		items = append(items, item)
	}
	return items, total, err
}
