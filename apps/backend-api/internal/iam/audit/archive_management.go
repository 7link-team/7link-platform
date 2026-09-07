package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"7link.com/platform/internal/iam/audit/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/minio/minio-go/v7"
)

// ArchiveStatus distinguishes age eligibility from verified archive completion.
type ArchiveStatus struct {
	Enabled        bool            `json:"enabled"`
	Policy         RetentionPolicy `json:"policy"`
	TotalEvents    int64           `json:"total_events"`
	EligibleEvents int64           `json:"eligible_events"`
	ArchivedEvents int64           `json:"archived_events"`
	PendingEvents  int64           `json:"pending_events"`
	QueuedJobs     int64           `json:"queued_jobs"`
	RunningJobs    int64           `json:"running_jobs"`
	FailedJobs     int64           `json:"failed_jobs"`
	CompletedJobs  int64           `json:"completed_jobs"`
}

func (s *Service) archiveStatus(ctx context.Context, tenantID guid.ID) (ArchiveStatus, error) {
	if tenantID < 0 {
		return ArchiveStatus{}, ErrArchiveInvalid
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return ArchiveStatus{}, err
	}
	policy, err := repo.getRetentionPolicy(ctx, tenantID)
	if err != nil {
		return ArchiveStatus{}, err
	}
	result := ArchiveStatus{Enabled: s.archive != nil, Policy: policy}
	db := repo.writer().WithContext(ctx)
	var groups []struct {
		State string
		Count int64
		Head  int64
	}
	if err := db.Table(repo.tables(db).ArchiveJobs.TableName()).
		Select("state, COUNT(*) AS count, MAX(head_sequence) AS head").Where("tenant_id = ?", tenantID).Group("state").Scan(&groups).Error; err != nil {
		return result, err
	}
	for _, group := range groups {
		switch group.State {
		case "queued":
			result.QueuedJobs = group.Count
		case "running":
			result.RunningJobs = group.Count
		case "failed":
			result.FailedJobs = group.Count
		case "complete":
			result.CompletedJobs, result.ArchivedEvents = group.Count, group.Head
		}
	}
	cutoff := s.now().UTC().AddDate(0, 0, -policy.ArchiveAfterDays)
	result.TotalEvents, result.EligibleEvents, err = repo.governanceCounts(ctx, tenantID, cutoff)
	if err != nil {
		return result, err
	}
	if err := db.Table(repo.tables(db).Events.TableName()).Where("tenant_id = ? AND sequence > ? AND created_at < ?", tenantID, result.ArchivedEvents, cutoff).
		Count(&result.PendingEvents).Error; err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) retryArchive(ctx context.Context, tenantID, id guid.ID) (ArchiveJob, error) {
	if tenantID < 0 || id <= 0 {
		return ArchiveJob{}, ErrArchiveInvalid
	}
	if s == nil || s.archive == nil {
		return ArchiveJob{}, ErrArchiveDisabled
	}
	var result model.ArchiveJob
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		job, err := repo.archiveJob(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if job.State == "queued" {
			result = job
			return nil
		}
		db := repo.writer().WithContext(ctx)
		clock := gormx.NowMillisExpr(db.Dialector.Name())
		updated := db.Table(repo.tables(db).ArchiveJobs.TableName()).Where("tenant_id = ? AND id = ?", tenantID, id).
			Where("state = 'failed' OR (state = 'running' AND lease_until <= " + clock + ")").
			Updates(map[string]any{"state": "queued", "next_attempt": 0, "lease_token": 0, "lease_until": 0, "error_code": "", "updated_at": s.now().UTC().Truncate(time.Millisecond)})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrArchiveBusy
		}
		_, err = s.appendTo(ctx, db, EventInput{
			TenantID: tenantID, PrincipalID: authn.PrincipalIDFromContext(ctx), EventType: "audit_archive_retry_requested",
			TargetType: "audit_archive", TargetID: id, Outcome: "success",
		})
		if err != nil {
			return err
		}
		result, err = repo.archiveJob(ctx, tenantID, id)
		return err
	})
	if err != nil {
		return ArchiveJob{}, err
	}
	return archiveJobView(result)
}

func (s *Service) verifyArchiveJob(ctx context.Context, tenantID, id guid.ID) (ArchiveObject, error) {
	if tenantID < 0 || id <= 0 {
		return ArchiveObject{}, ErrArchiveInvalid
	}
	if s == nil || s.archive == nil {
		return ArchiveObject{}, ErrArchiveDisabled
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return ArchiveObject{}, err
	}
	row, err := repo.archiveJob(ctx, tenantID, id)
	if err != nil {
		return ArchiveObject{}, err
	}
	if row.State != "complete" {
		return ArchiveObject{}, ErrArchiveBusy
	}
	job, err := archiveJobView(row)
	if err != nil {
		return ArchiveObject{}, err
	}
	if job.Object.Key != archivePrefix+"/"+tenantID.String()+"/"+id.String()+".ndjson" || job.Object.Size < 1 || job.Object.RetainUntil.Before(row.RetainUntil) {
		return ArchiveObject{}, ErrArchiveIntegrity
	}
	return s.archive.verifyArchive(ctx, *job.Object)
}

func (s *Service) requestArchiveDownload(ctx context.Context, tenantID, id guid.ID) (ArchiveObject, error) {
	object, err := s.verifyArchiveJob(ctx, tenantID, id)
	if err != nil {
		return ArchiveObject{}, err
	}
	_, err = s.Append(ctx, EventInput{
		TenantID: tenantID, PrincipalID: authn.PrincipalIDFromContext(ctx), EventType: "audit_archive_download_requested",
		TargetType: "audit_archive", TargetID: id, Outcome: "success",
	})
	return object, err
}

func (s *Service) writeArchiveDownload(ctx context.Context, object ArchiveObject, writer io.Writer) error {
	if s == nil || s.archive == nil {
		return ErrArchiveDisabled
	}
	if writer == nil {
		return ErrArchiveInvalid
	}
	reader, err := s.archive.client.GetObject(ctx, object.Bucket, object.Key, minio.GetObjectOptions{VersionID: object.VersionID})
	if err != nil {
		return archiveStoreError(err)
	}
	digest := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(writer, digest), io.LimitReader(reader, object.Size+1))
	if err := errors.Join(copyErr, reader.Close()); err != nil {
		return archiveStoreError(err)
	}
	if size != object.Size || hex.EncodeToString(digest.Sum(nil)) != object.SHA256 {
		return ErrArchiveIntegrity
	}
	return nil
}

func (r *Repository) archiveJobCount(ctx context.Context, tenantID guid.ID) (int64, error) {
	var count int64
	err := r.writer().WithContext(ctx).Table(r.tables(r.writer()).ArchiveJobs.TableName()).Where("tenant_id = ?", tenantID).Count(&count).Error
	return count, err
}
