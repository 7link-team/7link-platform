package provisioning

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"7link.com/platform/internal/infra/guid"
)

var (
	ErrTargetBusy     = errors.New("SCIM target has an unfinished operation")
	ErrTargetRetiring = errors.New("SCIM target is retiring")
)

// A gateway's error does not prove that its upstream stopped processing a
// write. Only explicit rejection statuses permit automatic release. In
// particular 408, every 5xx, redirects and unknown statuses remain unresolved.
func remoteRequestRejected(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusConflict,
		http.StatusGone, http.StatusPreconditionFailed, http.StatusRequestEntityTooLarge,
		http.StatusRequestURITooLong, http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

// targetOperation is a durable exclusion claim, not a lease. In particular,
// neither a timeout nor process death proves that a remote PUT has stopped.
type targetOperation struct {
	s                     *Service
	tenant, target, token guid.ID
	uncertain             bool
}

func (s *Service) claimTarget(ctx context.Context, tenant, target guid.ID, kind string, allowRetiring bool, resourceType string, resourceID guid.ID) (*targetOperation, error) {
	token, err := s.nextID()
	if err != nil {
		return nil, err
	}
	op := &targetOperation{s: s, tenant: tenant, target: target, token: token}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		targets := repo.names.Targets.String()
		q := repo.writer().WithContext(ctx).Table(targets).Where("tenant_id = ? AND id = ? AND operation_token = 0", tenant, target)
		if !allowRetiring {
			q = q.Where("retiring = ?", false)
		}
		result := q.Updates(map[string]any{"operation_token": token, "operation_kind": kind, "operation_resource_type": resourceType, "operation_resource_id": resourceID, "operation_started_at": s.now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			return nil
		}
		row, err := repo.getTarget(ctx, tenant, target)
		if err != nil {
			return err
		}
		if row.Retiring && !allowRetiring {
			return ErrTargetRetiring
		}
		return ErrTargetBusy
	})
	return op, err
}

func (op *targetOperation) finish(ctx context.Context, repo *Repository) error {
	result := repo.writer().WithContext(ctx).Table(repo.names.Targets.String()).
		Where("tenant_id = ? AND id = ? AND operation_token = ?", op.tenant, op.target, op.token).
		Updates(map[string]any{"operation_token": 0, "operation_kind": "", "operation_resource_type": "", "operation_resource_id": 0, "operation_started_at": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTargetBusy
	}
	return nil
}

func (op *targetOperation) release() {
	if op.uncertain {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Failure leaves the durable claim in place; it must never authorize a
	// competing request merely because this caller is going away.
	if err := op.finish(ctx, op.s.repo); err != nil {
		// The target may have been deleted by this very operation.
		if _, getErr := op.s.repo.getTarget(ctx, op.tenant, op.target); !errors.Is(getErr, ErrTargetMissing) {
			op.s.operationReleaseFailures.Add(1)
		}
	}
}

func (s *Service) markRetiring(ctx context.Context, tenant, target guid.ID) error {
	op, err := s.claimTarget(ctx, tenant, target, "retire", true, "", 0)
	if err != nil {
		return err
	}
	defer op.release()
	row, err := s.repo.getTarget(ctx, tenant, target)
	if err != nil {
		return err
	}
	if row.Status != TargetActive {
		return ErrTargetDisabled
	}
	if row.ServiceAccountID == nil {
		return ErrUnauthorized
	}
	if err := s.requireServiceAccount(ctx, s.repo, tenant, row.ServiceAccountID); err != nil {
		return err
	}
	return s.repo.writer().WithContext(ctx).Table(s.repo.names.Targets.String()).
		Where("tenant_id = ? AND id = ? AND operation_token = ?", tenant, target, op.token).
		Update("retiring", true).Error
}

func (r *Repository) targetResourcePage(ctx context.Context, target guid.ID, afterType string, afterID guid.ID, limit int) ([]targetResourceRow, error) {
	var rows []targetResourceRow
	err := r.writer().WithContext(ctx).Table(r.names.TargetResources.String()).
		Where("target_id = ? AND deleted_at IS NULL AND external_id <> ''", target).
		Where("resource_type > ? OR (resource_type = ? AND resource_id > ?)", afterType, afterType, afterID).
		Order("resource_type ASC, resource_id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("page SCIM mappings: %w", err)
	}
	return rows, nil
}
