package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit/model"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrArchiveDisabled = errors.New("audit archiving is not enabled")
	ErrArchiveEmpty    = errors.New("no audit events are ready for archiving")
	ErrArchiveNotFound = errors.New("audit archive job not found")
	ErrArchiveBusy     = errors.New("audit archive job is leased or awaiting retry")
)

// ArchiveJob is the tenant-visible execution state. Internal export snapshots,
// lease tokens and raw infrastructure errors are never returned to clients.
type ArchiveJob struct {
	ID            guid.ID        `json:"id"`
	TenantID      guid.ID        `json:"tenant_id"`
	AfterSequence int64          `json:"after_sequence"`
	HeadSequence  int64          `json:"head_sequence"`
	State         string         `json:"state"`
	Attempts      int64          `json:"attempts"`
	NextAttempt   int64          `json:"next_attempt"`
	ErrorCode     string         `json:"error_code,omitempty"`
	Object        *ArchiveObject `json:"object,omitempty"`
	RetainUntil   time.Time      `json:"retain_until"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func archiveJobView(row model.ArchiveJob) (ArchiveJob, error) {
	view := ArchiveJob{
		ID: row.ID, TenantID: row.TenantID, AfterSequence: row.AfterSequence, HeadSequence: row.HeadSequence,
		State: row.State, Attempts: row.Attempts, NextAttempt: row.NextAttempt, ErrorCode: row.ErrorCode,
		RetainUntil: row.RetainUntil.UTC(), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.State == "complete" {
		var object ArchiveObject
		if err := json.Unmarshal([]byte(row.Object), &object); err != nil || object.VersionID == "" {
			return ArchiveJob{}, ErrArchiveIntegrity
		}
		view.Object = &object
	}
	return view, nil
}

func (s *Service) ListArchiveJobs(ctx context.Context, tenantID guid.ID, offset, limit int) ([]ArchiveJob, error) {
	if tenantID <= 0 {
		return nil, ErrArchiveInvalid
	}
	return s.listArchiveJobs(ctx, tenantID, offset, limit)
}

func (s *Service) listArchiveJobs(ctx context.Context, tenantID guid.ID, offset, limit int) ([]ArchiveJob, error) {
	if tenantID < 0 || offset < 0 || limit < 1 || limit > 200 {
		return nil, ErrArchiveInvalid
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return nil, err
	}
	var rows []model.ArchiveJob
	if err := repo.writer().WithContext(ctx).Table(repo.tables(repo.writer()).ArchiveJobs.TableName()).
		Where("tenant_id = ?", tenantID).Order("after_sequence DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	jobs := make([]ArchiveJob, 0, len(rows))
	for _, row := range rows {
		job, err := archiveJobView(row)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// RequestArchive freezes the next contiguous, old-enough chain segment. A
// younger event stops selection, even if later events have backdated clocks:
// archive cursors must never skip a row that could become eligible later.
func (s *Service) RequestArchive(ctx context.Context, tenantID guid.ID) (ArchiveJob, error) {
	if tenantID <= 0 {
		return ArchiveJob{}, ErrArchiveInvalid
	}
	return s.requestArchive(ctx, tenantID)
}

func (s *Service) requestArchive(ctx context.Context, tenantID guid.ID) (ArchiveJob, error) {
	if tenantID < 0 {
		return ArchiveJob{}, ErrArchiveInvalid
	}
	if s == nil || s.archive == nil {
		return ArchiveJob{}, ErrArchiveDisabled
	}
	var result model.ArchiveJob
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		db := repo.writer().WithContext(ctx)
		table := repo.tables(db).ArchiveJobs.TableName()
		var latest model.ArchiveJob
		err := db.Table(table).Where("tenant_id = ?", tenantID).Order("after_sequence DESC").Take(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && latest.State != "complete" {
			result = latest
			return nil
		}
		policy, err := repo.getRetentionPolicy(ctx, tenantID)
		if err != nil {
			return err
		}
		now := s.now().UTC().Truncate(time.Millisecond)
		cutoff := now.AddDate(0, 0, -policy.ArchiveAfterDays)
		after := latest.HeadSequence
		var rows []model.Event
		if err := db.Table(repo.tables(db).Events.TableName()).Where("tenant_id = ? AND sequence > ?", tenantID, after).
			Order("sequence ASC").Limit(exportBatchSize).Find(&rows).Error; err != nil {
			return err
		}
		var head model.Event
		for _, row := range rows {
			if !row.CreatedAt.Before(cutoff) {
				break
			}
			head = row
		}
		if head.Sequence == 0 {
			return ErrArchiveEmpty
		}
		integrity, err := s.verifyPrefix(ctx, repo, tenantID, head.Sequence)
		if err != nil {
			return err
		}
		if !integrity.Valid || integrity.HeadHash != normalizeStoredHash(head.EventHash) {
			return ErrIntegrity
		}
		snapshot := ExportSnapshot{Filter: Filter{TenantID: tenantID}, Integrity: integrity, GeneratedAt: now, AfterSequence: after}
		if after > 0 {
			var previous ExportSnapshot
			if err := json.Unmarshal([]byte(latest.Snapshot), &previous); err != nil {
				return ErrArchiveIntegrity
			}
			snapshot.PreviousHash = previous.Integrity.HeadHash
		}
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		id, err := repo.nextID()
		if err != nil {
			return err
		}
		result = model.ArchiveJob{
			ID: id, TenantID: tenantID, AfterSequence: after, HeadSequence: head.Sequence,
			Snapshot: string(payload), State: "queued", Object: "{}",
			RetainUntil: now.AddDate(0, 0, policy.MinDays), CreatedAt: now, UpdatedAt: now,
		}
		insert := db.Table(table).Clauses(clause.OnConflict{DoNothing: true}).Create(&result)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			// Discard the attempted ID: GORM otherwise adds it as a primary-key
			// filter. A locking read sees the winner under MySQL REPEATABLE READ.
			result = model.ArchiveJob{}
			read := db.Table(table).Where("tenant_id = ? AND after_sequence = ?", tenantID, after)
			if db.Dialector.Name() != "sqlite" {
				read = read.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate})
			}
			return read.Take(&result).Error
		}
		_, err = s.appendTo(ctx, db, contextEventInput(ctx, tenantID, "audit_archive_requested", "audit_archive", id,
			map[string]any{"after_sequence": after, "head_sequence": head.Sequence}))
		return err
	})
	if err != nil {
		return ArchiveJob{}, err
	}
	return archiveJobView(result)
}

func (r *Repository) archiveJob(ctx context.Context, tenantID, id guid.ID) (model.ArchiveJob, error) {
	var job model.ArchiveJob
	err := r.writer().WithContext(ctx).Table(r.tables(r.writer()).ArchiveJobs.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return job, ErrArchiveNotFound
	}
	return job, err
}

// RunArchive claims a database-clock lease before external I/O. Completion is
// fenced by its unique token, so an expired worker cannot acknowledge a job
// claimed by another replica. The immutable key makes their uploads converge.
func (s *Service) RunArchive(ctx context.Context, tenantID, id guid.ID, timeout time.Duration) (ArchiveJob, error) {
	if tenantID <= 0 {
		return ArchiveJob{}, ErrArchiveInvalid
	}
	return s.runArchive(ctx, tenantID, id, timeout)
}

func (s *Service) runArchive(ctx context.Context, tenantID, id guid.ID, timeout time.Duration) (ArchiveJob, error) {
	if tenantID < 0 || id <= 0 || timeout < time.Second || timeout > time.Hour {
		return ArchiveJob{}, ErrArchiveInvalid
	}
	if s == nil || s.archive == nil {
		return ArchiveJob{}, ErrArchiveDisabled
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return ArchiveJob{}, err
	}
	job, err := repo.archiveJob(ctx, tenantID, id)
	if err != nil {
		return ArchiveJob{}, err
	}
	if job.State == "complete" {
		return archiveJobView(job)
	}
	token, err := repo.nextID()
	if err != nil {
		return ArchiveJob{}, err
	}
	db := repo.writer().WithContext(ctx)
	table := repo.tables(db).ArchiveJobs.TableName()
	clock := gormx.NowMillisExpr(db.Dialector.Name())
	claim := db.Table(table).Where("tenant_id = ? AND id = ?", tenantID, id).
		Where("((state IN ('queued', 'failed') AND next_attempt <= " + clock + ") OR (state = 'running' AND lease_until <= " + clock + "))").
		Updates(map[string]any{
			"state": "running", "attempts": gorm.Expr("attempts + 1"), "lease_token": token,
			"lease_until": gorm.Expr(clock+" + ?", (timeout + 30*time.Second).Milliseconds()), "error_code": "",
			"updated_at": s.now().UTC().Truncate(time.Millisecond),
		})
	if claim.Error != nil {
		return ArchiveJob{}, claim.Error
	}
	if claim.RowsAffected != 1 {
		return ArchiveJob{}, ErrArchiveBusy
	}
	job, err = repo.archiveJob(ctx, tenantID, id)
	if err != nil {
		return ArchiveJob{}, err
	}
	if job.LeaseToken != token {
		return ArchiveJob{}, ErrArchiveBusy
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	var snapshot ExportSnapshot
	var object ArchiveObject
	if err = json.Unmarshal([]byte(job.Snapshot), &snapshot); err == nil {
		if snapshot.Filter.TenantID != tenantID || snapshot.AfterSequence != job.AfterSequence || snapshot.Integrity.HeadSequence != job.HeadSequence || !snapshot.GeneratedAt.Equal(job.CreatedAt) {
			err = ErrArchiveIntegrity
		} else {
			object, err = s.writeArchiveSnapshot(runCtx, s.archive, snapshot, id, job.RetainUntil)
		}
	} else {
		err = ErrArchiveIntegrity
	}
	cancel()
	// Cancellation must not erase the attempt result. This bounded context only
	// records state; it cannot continue an upload after the caller has canceled.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	finishErr := s.finishArchive(finishCtx, job, object, err)
	if finishErr != nil {
		return ArchiveJob{}, errors.Join(err, finishErr)
	}
	if err != nil {
		return ArchiveJob{}, err
	}
	completed, err := repo.archiveJob(finishCtx, tenantID, id)
	if err != nil {
		return ArchiveJob{}, err
	}
	return archiveJobView(completed)
}

func (s *Service) finishArchive(ctx context.Context, job model.ArchiveJob, object ArchiveObject, runErr error) error {
	return s.repo.runInTx(ctx, func(repo *Repository) error {
		db := repo.writer().WithContext(ctx)
		clock := gormx.NowMillisExpr(db.Dialector.Name())
		updates := map[string]any{"state": "complete", "error_code": "", "lease_token": 0, "lease_until": 0, "next_attempt": 0, "updated_at": s.now().UTC().Truncate(time.Millisecond)}
		if runErr == nil {
			payload, err := json.Marshal(object)
			if err != nil {
				return err
			}
			updates["object"] = string(payload)
		} else {
			updates["state"], updates["error_code"] = "failed", archiveFailureCode(runErr)
			delay := time.Minute << min(max(job.Attempts-1, 0), 6)
			updates["next_attempt"] = gorm.Expr(clock+" + ?", min(delay, time.Hour).Milliseconds())
		}
		result := db.Table(repo.tables(db).ArchiveJobs.TableName()).
			Where("tenant_id = ? AND id = ? AND state = 'running' AND lease_token = ? AND lease_until > "+clock, job.TenantID, job.ID, job.LeaseToken).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrArchiveBusy
		}
		eventType, outcome := "audit_archive_completed", "success"
		detail := map[string]any{"after_sequence": job.AfterSequence, "head_sequence": job.HeadSequence, "attempt": job.Attempts}
		if runErr == nil {
			detail["sha256"], detail["version_id"] = object.SHA256, object.VersionID
		} else {
			eventType, outcome = "audit_archive_failed", "failure"
			detail["error_code"] = updates["error_code"]
		}
		_, err := s.appendTo(ctx, db, EventInput{TenantID: job.TenantID, EventType: eventType, TargetType: "audit_archive", TargetID: job.ID, Outcome: outcome, Detail: detail})
		if err != nil {
			return fmt.Errorf("record archive result: %w", err)
		}
		return nil
	})
}
