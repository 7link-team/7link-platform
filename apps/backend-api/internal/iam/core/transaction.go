package core

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
)

type transactionRecord struct {
	audit.DomainEvent
	PolicyChanged bool
	SkipAudit     bool
}

type transactionCoordinator struct {
	repo  *Repository
	audit audit.Appender
}

func newTransactionCoordinator(repo *Repository, appender audit.Appender) *transactionCoordinator {
	return &transactionCoordinator{repo: repo, audit: appender}
}

func (coordinator *transactionCoordinator) Run(
	ctx context.Context,
	record *transactionRecord,
	mutate func(*Repository) error,
) error {
	if coordinator == nil || coordinator.repo == nil || coordinator.audit == nil || record == nil || mutate == nil {
		return errors.New("invalid IAM write transaction")
	}
	return coordinator.repo.runInTx(ctx, func(repo *Repository) error {
		if err := mutate(repo); err != nil {
			return err
		}
		if record.PolicyChanged && !record.TenantID.Zero() {
			if err := repo.bumpPolicyRevision(ctx, record.TenantID); err != nil {
				return err
			}
		}
		if record.SkipAudit {
			return nil
		}
		return coordinator.audit(ctx, repo.writer(), record.DomainEvent)
	})
}

func newAuditRecord(
	ctx context.Context,
	tenantID guid.ID,
	eventType, targetType string,
	targetID guid.ID,
) *transactionRecord {
	return &transactionRecord{DomainEvent: audit.NewDomainEvent(ctx, tenantID, eventType, targetType, targetID)}
}

func (r *Repository) bumpPolicyRevision(ctx context.Context, tenantID guid.ID) error {
	if r == nil || r.revisions == nil {
		return ErrUnavailable
	}
	if err := r.revisions.Bump(ctx, r.writer(), tenantID, r.now()); err != nil {
		return fmt.Errorf("bump IAM policy revision: %w", err)
	}
	return nil
}
