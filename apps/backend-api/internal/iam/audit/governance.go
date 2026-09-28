package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultMinRetentionDays = 365
	defaultArchiveAfterDays = 730
	maxRetentionDays        = 36500
	// maxAnchorPage is the widest anchor window the report will carry. It
	// mirrors the maximum the governance input declares, so a service caller
	// cannot ask for more than an HTTP caller is allowed to.
	maxAnchorPage = 200
)

var ErrRetentionInvalid = errors.New("invalid audit retention policy")

type RetentionPolicy struct {
	TenantID         guid.ID   `json:"tenant_id"`
	MinDays          int       `json:"min_days"`
	ArchiveAfterDays int       `json:"archive_after_days"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type Governance struct {
	Policy       RetentionPolicy `json:"policy"`
	Integrity    Integrity       `json:"integrity"`
	Anchors      []Anchor        `json:"anchors,omitempty"`
	TotalEvents  int64           `json:"total_events"`
	ArchiveReady int64           `json:"archive_ready_events"`
	Anchored     int64           `json:"anchored_events"`
}

func (s *Service) GetRetentionPolicy(ctx context.Context, tenantID guid.ID) (RetentionPolicy, error) {
	if tenantID.Zero() {
		return RetentionPolicy{}, ErrRetentionInvalid
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return RetentionPolicy{}, err
	}
	return repo.getRetentionPolicy(ctx, tenantID)
}

func (s *Service) SetRetentionPolicy(ctx context.Context, tenantID guid.ID, minDays, archiveAfterDays int) (RetentionPolicy, error) {
	if tenantID <= 0 {
		return RetentionPolicy{}, ErrRetentionInvalid
	}
	return s.setRetentionPolicy(ctx, tenantID, minDays, archiveAfterDays)
}

func (s *Service) setRetentionPolicy(ctx context.Context, tenantID guid.ID, minDays, archiveAfterDays int) (RetentionPolicy, error) {
	if tenantID < 0 || minDays < 1 || minDays > maxRetentionDays || archiveAfterDays < minDays || archiveAfterDays > maxRetentionDays {
		return RetentionPolicy{}, ErrRetentionInvalid
	}
	if s == nil || s.repo == nil || s.now == nil {
		return RetentionPolicy{}, errRepositoryUnavailable
	}
	row := model.RetentionPolicy{
		TenantID: tenantID, MinDays: minDays, ArchiveAfterDays: archiveAfterDays,
		UpdatedAt: s.now().UTC().Truncate(time.Millisecond),
	}
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.setRetentionPolicy(ctx, row); err != nil {
			return err
		}
		_, err := s.appendTo(ctx, repo.writer(), contextEventInput(ctx, tenantID, "audit_retention_policy_updated",
			"audit_retention_policy", tenantID, map[string]any{"min_days": minDays, "archive_after_days": archiveAfterDays}))
		if err != nil {
			return fmt.Errorf("audit retention policy change: %w", err)
		}
		return nil
	})
	if err != nil {
		return RetentionPolicy{}, err
	}
	return policyFromRow(row), nil
}

func (s *Service) Governance(ctx context.Context, tenantID guid.ID) (Governance, error) {
	if tenantID.Zero() {
		return Governance{}, ErrRetentionInvalid
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return Governance{}, err
	}
	policy, err := repo.getRetentionPolicy(ctx, tenantID)
	if err != nil {
		return Governance{}, err
	}
	integrity, err := s.verify(ctx, repo, tenantID)
	if err != nil {
		return Governance{}, err
	}
	var anchors []Anchor
	if s.anchor != nil {
		anchors, err = s.anchor.List(ctx, tenantID)
		if err != nil {
			return Governance{}, anchorStoreError(err)
		}
		status, verifyErr := s.verifyAnchors(ctx, repo, tenantID)
		if verifyErr != nil {
			return Governance{}, verifyErr
		}
		integrity.Anchor = status
	}
	total, archiveReady, err := repo.governanceCounts(ctx, tenantID, s.now().UTC().AddDate(0, 0, -policy.ArchiveAfterDays))
	if err != nil {
		return Governance{}, err
	}
	report := Governance{Policy: policy, Integrity: integrity, Anchors: anchors, TotalEvents: total, ArchiveReady: archiveReady}
	if len(anchors) > 0 {
		report.Anchored = anchors[len(anchors)-1].HeadSequence
	}
	return report, nil
}

// GovernancePage returns the governance report with its anchor chain bounded to
// one window, plus the number of anchors before pagination. The chain is the one
// open-ended part of the report: it gains an entry on every anchoring run, while
// every other field is a fixed-size summary. Anchored is still derived from the
// whole chain, so a bounded report names the same anchored head as an unbounded
// one rather than the head of whichever page was asked for.
func (s *Service) GovernancePage(ctx context.Context, tenantID guid.ID, offset, limit int) (Governance, int64, error) {
	report, err := s.Governance(ctx, tenantID)
	if err != nil {
		return Governance{}, 0, err
	}
	total := int64(len(report.Anchors))
	report.Anchors = pageAnchors(report.Anchors, offset, limit)
	return report, total, nil
}

// pageAnchors returns one window of the anchor chain. A zero limit is unbounded
// here, which is why the governance input declares a minimum of one: the bound a
// caller is held to has to come from the transport, where the caller actually is.
func pageAnchors(anchors []Anchor, offset, limit int) []Anchor {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	if limit > maxAnchorPage {
		limit = maxAnchorPage
	}
	if offset >= len(anchors) {
		return nil
	}
	window := anchors[offset:]
	if limit > 0 && limit < len(window) {
		window = window[:limit]
	}
	return window
}

func (r *Repository) getRetentionPolicy(ctx context.Context, tenantID guid.ID) (RetentionPolicy, error) {
	db := r.writer()
	if db == nil {
		return RetentionPolicy{}, errRepositoryUnavailable
	}
	var row model.RetentionPolicy
	err := db.WithContext(ctx).Table(r.tables(db).RetentionPolicies.TableName()).Where("tenant_id = ?", tenantID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RetentionPolicy{TenantID: tenantID, MinDays: defaultMinRetentionDays, ArchiveAfterDays: defaultArchiveAfterDays}, nil
	}
	if err != nil {
		return RetentionPolicy{}, fmt.Errorf("read audit retention policy: %w", err)
	}
	return policyFromRow(row), nil
}

func (r *Repository) setRetentionPolicy(ctx context.Context, row model.RetentionPolicy) error {
	db := r.writer()
	if db == nil {
		return errRepositoryUnavailable
	}
	err := db.WithContext(ctx).Table(r.tables(db).RetentionPolicies.TableName()).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"min_days", "archive_after_days", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save audit retention policy: %w", err)
	}
	return nil
}

func (r *Repository) governanceCounts(ctx context.Context, tenantID guid.ID, cutoff time.Time) (int64, int64, error) {
	db := r.writer()
	if db == nil {
		return 0, 0, errRepositoryUnavailable
	}
	table := r.tables(db).Events.TableName()
	var total, archiveReady int64
	if err := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return 0, 0, fmt.Errorf("count audit events: %w", err)
	}
	if err := db.WithContext(ctx).Table(table).Where("tenant_id = ? AND created_at < ?", tenantID, cutoff).Count(&archiveReady).Error; err != nil {
		return 0, 0, fmt.Errorf("count archive-ready audit events: %w", err)
	}
	return total, archiveReady, nil
}

func policyFromRow(row model.RetentionPolicy) RetentionPolicy {
	return RetentionPolicy{
		TenantID: row.TenantID, MinDays: row.MinDays,
		ArchiveAfterDays: row.ArchiveAfterDays, UpdatedAt: row.UpdatedAt.UTC(),
	}
}
