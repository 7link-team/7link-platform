package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"7link.com/platform/internal/iam/audit/model"
	"7link.com/platform/internal/infra/guid"
)

func validateArchiveWorker(config ArchiveConfig) error {
	if config.Interval < time.Second || config.Timeout < time.Second || config.Timeout > time.Hour {
		return fmt.Errorf("audit archive configuration: interval must be at least one second; timeout must be between one second and one hour")
	}
	return nil
}

func (m *Module) runArchives(ctx context.Context, service *Service, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(m.config.Archive.Interval)
	defer ticker.Stop()
	for {
		if err := service.sweepArchives(ctx, m.config.Archive.Timeout); err != nil && ctx.Err() == nil {
			service.archiveSweepFailures.Add(1)
			slog.ErrorContext(ctx, "audit archive sweep failed", "error_code", "database_unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) sweepArchives(ctx context.Context, timeout time.Duration) error {
	repo, err := s.pinnedRepository()
	if err != nil {
		return err
	}
	cursor := guid.ID(-1)
	for {
		var heads []model.Head
		if err := repo.writer().WithContext(ctx).Table(repo.tables(repo.writer()).Heads.TableName()).
			Where("tenant_id > ?", cursor).Order("tenant_id ASC").Limit(100).Find(&heads).Error; err != nil {
			return err
		}
		for _, head := range heads {
			if err := ctx.Err(); err != nil {
				return err
			}
			cursor = head.TenantID
			attemptCtx, cancel := context.WithTimeout(ctx, timeout)
			job, err := s.requestArchive(attemptCtx, head.TenantID)
			if errors.Is(err, ErrArchiveEmpty) {
				cancel()
				continue
			}
			if err == nil {
				_, err = s.runArchive(attemptCtx, head.TenantID, job.ID, timeout)
			}
			cancel()
			if err != nil && !errors.Is(err, ErrArchiveBusy) && ctx.Err() == nil {
				s.archiveAttemptFailures.Add(1)
				slog.ErrorContext(ctx, "audit archive attempt failed", "tenant_id", head.TenantID.String(), "job_id", job.ID.String(), "error_code", archiveFailureCode(err))
			}
		}
		if len(heads) < 100 {
			return nil
		}
	}
}

func archiveFailureCode(err error) string {
	if errors.Is(err, ErrIntegrity) || errors.Is(err, ErrArchiveIntegrity) || errors.Is(err, ErrArchiveInvalid) {
		return "integrity_failed"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "interrupted"
	}
	return "storage_unavailable"
}

func (m *Module) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.workerMu.Lock()
	cancel, done := m.workerCancel, m.workerDone
	m.workerMu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		m.workerMu.Lock()
		if m.workerDone == done {
			m.workerCancel, m.workerDone = nil, nil
		}
		m.workerMu.Unlock()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop audit archive worker: %w", ctx.Err())
	}
}
