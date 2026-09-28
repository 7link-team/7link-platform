package governance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/governance/model"
	"7link.com/platform/internal/infra/guid"
)

const (
	ReviewStatusOpen      = "open"
	ReviewStatusCompleted = "completed"
	ReviewStatusCancelled = "cancelled" //nolint:misspell // Frozen source state value.
	ReviewStatusExpired   = "expired"

	ReviewDecisionPending = "pending"
	ReviewDecisionKeep    = "keep"
	ReviewDecisionRevoke  = "revoke"

	ReviewGrantPermanent    = "permanent"
	ReviewGrantTemporary    = "temporary"
	ReviewGrantGroup        = "group"
	ReviewGrantEntity       = "entity"
	ReviewGrantDynamicGroup = "dynamic_group"
	ReviewGrantRelationship = "relationship"

	maxReviewDuration = 365 * 24 * time.Hour
)

var (
	ErrInvalidReview           = errors.New("invalid access review")
	ErrReviewNotFound          = errors.New("access review not found")
	ErrReviewItemNotFound      = errors.New("access review item not found")
	ErrReviewEmpty             = errors.New("access review has no eligible grants")
	ErrReviewStateConflict     = errors.New("access review state conflict")
	ErrReviewIncomplete        = errors.New("access review has pending items")
	ErrReviewExpired           = errors.New("access review expired")
	ErrReviewSelfDecision      = errors.New("reviewer cannot decide their own access")
	ErrReviewLastAdministrator = errors.New("review cannot revoke the last tenant administrator")
	ErrReviewDynamicDerived    = errors.New("rule-derived group access cannot be revoked per user; change the group rule or remove the role binding from the group")
)

type AccessReview struct {
	ID       guid.ID `json:"id"`
	TenantID guid.ID `json:"tenant_id"`
	Name     string  `json:"name"`
	// OwnerID is the principal who owns the review, not an organization root.
	OwnerID     guid.ID            `json:"owner_id" doc:"principal who owns the review (an active member of the tenant)"`
	Status      string             `json:"status"`
	DueAt       time.Time          `json:"due_at"`
	CompletedAt *time.Time         `json:"completed_at,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	Total       int                `json:"total"`
	Pending     int                `json:"pending"`
	Kept        int                `json:"kept"`
	Revoked     int                `json:"revoked"`
	Items       []AccessReviewItem `json:"items"`
}

type AccessReviewItem struct {
	ID             guid.ID    `json:"id"`
	ReviewID       guid.ID    `json:"review_id"`
	PrincipalID    guid.ID    `json:"principal_id"`
	PrincipalName  string     `json:"principal_name"`
	RoleID         guid.ID    `json:"role_id"`
	RoleName       string     `json:"role_name"`
	GrantType      string     `json:"grant_type"`
	GrantID        guid.ID    `json:"grant_id,omitempty"`
	GrantCreatedAt time.Time  `json:"grant_created_at"`
	GrantExpiresAt *time.Time `json:"grant_expires_at,omitempty"`
	Decision       string     `json:"decision"`
	DecidedBy      guid.ID    `json:"decided_by,omitempty"`
	DecisionNote   string     `json:"decision_note,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
}

type CreateAccessReview struct {
	Name  string
	DueAt time.Time
}

type reviewableGrantRow struct {
	PrincipalID    guid.ID    `gorm:"column:principal_id"`
	PrincipalName  string     `gorm:"column:principal_name"`
	RoleID         guid.ID    `gorm:"column:role_id"`
	RoleName       string     `gorm:"column:role_name"`
	GrantType      string     `gorm:"column:grant_type"`
	GrantID        *guid.ID   `gorm:"column:grant_id"`
	GrantCreatedAt time.Time  `gorm:"column:grant_created_at"`
	GrantExpiresAt *time.Time `gorm:"column:grant_expires_at"`
}

type accessReviewCountRow struct {
	ReviewID guid.ID `gorm:"column:review_id"`
	Total    int     `gorm:"column:total"`
	Pending  int     `gorm:"column:pending"`
	Kept     int     `gorm:"column:kept"`
	Revoked  int     `gorm:"column:revoked"`
}

func (s *Service) CreateReview(ctx context.Context, tenantID, ownerID guid.ID, input CreateAccessReview) (AccessReview, error) {
	input.Name = strings.TrimSpace(input.Name)
	now := s.now().UTC().Truncate(time.Millisecond)
	input.DueAt = input.DueAt.UTC().Truncate(time.Millisecond)
	if tenantID.Zero() || ownerID.Zero() || len(input.Name) < 3 || len(input.Name) > 128 ||
		!input.DueAt.After(now) || input.DueAt.After(now.Add(maxReviewDuration)) {
		return AccessReview{}, ErrInvalidReview
	}
	reviewID, err := s.repo.nextID()
	if err != nil {
		return AccessReview{}, fmt.Errorf("generate access review id: %w", err)
	}
	row := model.AccessReview{
		TenantID: tenantID, ID: reviewID, Name: input.Name, OwnerID: ownerID,
		Status: ReviewStatusOpen, DueAt: input.DueAt, CreatedAt: now, UpdatedAt: now,
	}
	items := make([]model.AccessReviewItem, 0)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		active, err := repo.activeTenantMember(ctx, tenantID, ownerID)
		if err != nil {
			return err
		}
		if !active {
			return ErrRequesterInactive
		}
		grants, err := repo.listReviewableGrants(ctx, tenantID, now)
		if err != nil {
			return err
		}
		if len(grants) == 0 {
			return ErrReviewEmpty
		}
		tables := repo.tables(repo.writer())
		if err := repo.writer().WithContext(ctx).Table(tables.AccessReviews.TableName()).Create(&row).Error; err != nil {
			return fmt.Errorf("insert access review: %w", err)
		}
		for _, grant := range grants {
			itemID, err := repo.nextID()
			if err != nil {
				return fmt.Errorf("generate access review item id: %w", err)
			}
			items = append(items, model.AccessReviewItem{
				TenantID: tenantID, ReviewID: reviewID, ID: itemID,
				PrincipalID: grant.PrincipalID, PrincipalName: grant.PrincipalName,
				RoleID: grant.RoleID, RoleName: grant.RoleName, GrantType: grant.GrantType,
				GrantID: grant.GrantID, GrantCreatedAt: grant.GrantCreatedAt.UTC(),
				GrantExpiresAt: utcPointer(grant.GrantExpiresAt), Decision: ReviewDecisionPending,
			})
		}
		if err := repo.writer().WithContext(ctx).Table(tables.AccessReviewItems.TableName()).Create(&items).Error; err != nil {
			return fmt.Errorf("insert access review items: %w", err)
		}
		event := audit.NewDomainEvent(ctx, tenantID, "access_review_created", "access_review", reviewID)
		event.Detail["owner_id"] = ownerID
		event.Detail["item_count"] = len(items)
		event.Detail["due_at"] = input.DueAt.Format(time.RFC3339Nano)
		return s.audit(ctx, repo.writer(), event)
	})
	if err != nil {
		return AccessReview{}, fmt.Errorf("create access review: %w", err)
	}
	return reviewFromRows(row, items, now), nil
}

func (s *Service) ListReviews(ctx context.Context, tenantID guid.ID) ([]AccessReview, error) {
	if tenantID.Zero() || s == nil || s.repo == nil {
		return nil, ErrInvalidReview
	}
	db := s.repo.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	rows := make([]model.AccessReview, 0)
	if err := db.WithContext(ctx).Table(s.repo.tables(db).AccessReviews.TableName()).
		Where("tenant_id = ?", tenantID).Order("created_at DESC, id DESC").Limit(100).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list access reviews: %w", err)
	}
	counts, err := s.repo.reviewCounts(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	reviews := make([]AccessReview, 0, len(rows))
	for _, row := range rows {
		review := reviewFromRows(row, nil, now)
		if count, ok := counts[row.ID]; ok {
			review.Total, review.Pending, review.Kept, review.Revoked = count.Total, count.Pending, count.Kept, count.Revoked
		}
		reviews = append(reviews, review)
	}
	return reviews, nil
}

func (s *Service) GetReview(ctx context.Context, tenantID, reviewID guid.ID) (AccessReview, error) {
	if tenantID.Zero() || reviewID.Zero() || s == nil || s.repo == nil {
		return AccessReview{}, ErrInvalidReview
	}
	row, err := s.repo.getReview(ctx, tenantID, reviewID)
	if err != nil {
		return AccessReview{}, err
	}
	items, err := s.repo.listReviewItems(ctx, tenantID, reviewID)
	if err != nil {
		return AccessReview{}, err
	}
	return reviewFromRows(row, items, s.now().UTC()), nil
}

func (s *Service) DecideReviewItem(ctx context.Context, tenantID, reviewID, itemID, actorID guid.ID, decision, note string) (AccessReviewItem, error) {
	note = strings.TrimSpace(note)
	if tenantID.Zero() || reviewID.Zero() || itemID.Zero() || actorID.Zero() ||
		(decision != ReviewDecisionKeep && decision != ReviewDecisionRevoke) || len(note) > 500 {
		return AccessReviewItem{}, ErrInvalidReview
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	var updated model.AccessReviewItem
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		review, err := repo.getReview(ctx, tenantID, reviewID)
		if err != nil {
			return err
		}
		if review.Status != ReviewStatusOpen {
			return ErrReviewStateConflict
		}
		if !review.DueAt.After(now) {
			return ErrReviewExpired
		}
		item, err := repo.getReviewItem(ctx, tenantID, reviewID, itemID)
		if err != nil {
			return err
		}
		if item.Decision != ReviewDecisionPending {
			return ErrReviewStateConflict
		}
		if item.PrincipalID == actorID {
			return ErrReviewSelfDecision
		}
		policyChanged := false
		if decision == ReviewDecisionRevoke {
			policyChanged, err = s.revokeReviewedGrant(ctx, repo, item)
			if errors.Is(err, core.ErrLastTenantAdministrator) {
				return ErrReviewLastAdministrator
			}
			if err != nil {
				return err
			}
		}
		tables := repo.tables(repo.writer())
		result := repo.writer().WithContext(ctx).Table(tables.AccessReviewItems.TableName()).
			Where("tenant_id = ? AND review_id = ? AND id = ? AND decision = ?", tenantID, reviewID, itemID, ReviewDecisionPending).
			Updates(map[string]any{"decision": decision, "decided_by": actorID, "decision_note": note, "decided_at": now})
		if result.Error != nil {
			return fmt.Errorf("decide access review item: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrReviewStateConflict
		}
		result = repo.writer().WithContext(ctx).Table(tables.AccessReviews.TableName()).
			Where("tenant_id = ? AND id = ? AND status = ?", tenantID, reviewID, ReviewStatusOpen).
			Update("updated_at", now)
		if result.Error != nil {
			return fmt.Errorf("touch access review: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrReviewStateConflict
		}
		if policyChanged {
			if err := repo.bumpPolicy(ctx, tenantID, now); err != nil {
				return err
			}
		}
		item.Decision, item.DecisionNote = decision, note
		item.DecidedBy, item.DecidedAt = idPointer(actorID), timePointer(now)
		updated = item
		eventType := "access_review_item_kept"
		if decision == ReviewDecisionRevoke {
			eventType = "access_review_item_revoked"
		}
		event := audit.NewDomainEvent(ctx, tenantID, eventType, "access_review_item", itemID)
		event.Detail["review_id"] = reviewID
		event.Detail["principal_id"] = item.PrincipalID
		event.Detail["role_id"] = item.RoleID
		event.Detail["grant_type"] = item.GrantType
		event.Detail["grant_changed"] = policyChanged
		return s.audit(ctx, repo.writer(), event)
	})
	if err != nil {
		return AccessReviewItem{}, fmt.Errorf("decide access review item: %w", err)
	}
	return reviewItemFromRow(updated), nil
}

func (s *Service) revokeReviewedGrant(ctx context.Context, repo *Repository, item model.AccessReviewItem) (bool, error) {
	switch item.GrantType {
	case ReviewGrantPermanent:
		return s.grants.RemovePermanent(ctx, repo.writer(), item.TenantID, item.RoleID, item.PrincipalID, item.GrantCreatedAt)
	case ReviewGrantTemporary:
		if item.GrantID == nil {
			return false, ErrInvalidReview
		}
		if err := repo.lockPolicy(ctx, item.TenantID); err != nil {
			return false, err
		}
		return s.grants.Revoke(ctx, repo.writer(), item.TenantID, *item.GrantID)
	case ReviewGrantGroup:
		if item.GrantID == nil {
			return false, ErrInvalidReview
		}
		return s.grants.RemoveGroupMembership(ctx, repo.writer(), item.TenantID, *item.GrantID, item.PrincipalID)
	case ReviewGrantEntity:
		if item.GrantID == nil {
			return false, ErrInvalidReview
		}
		return s.grants.RemoveEntityRoleBinding(ctx, repo.writer(), item.TenantID, *item.GrantID, item.RoleID, item.PrincipalID)
	case ReviewGrantDynamicGroup:
		return false, ErrReviewDynamicDerived
	case ReviewGrantRelationship:
		if item.GrantID == nil {
			return false, ErrInvalidReview
		}
		return repo.removeRelationship(ctx, item.TenantID, item.PrincipalID, *item.GrantID)
	default:
		return false, ErrInvalidReview
	}
}

func (s *Service) CompleteReview(ctx context.Context, tenantID, reviewID, actorID guid.ID) (AccessReview, error) {
	return s.finishReview(ctx, tenantID, reviewID, actorID, ReviewStatusCompleted)
}

func (s *Service) CancelReview(ctx context.Context, tenantID, reviewID, actorID guid.ID) (AccessReview, error) {
	return s.finishReview(ctx, tenantID, reviewID, actorID, ReviewStatusCancelled)
}

func (s *Service) finishReview(ctx context.Context, tenantID, reviewID, actorID guid.ID, status string) (AccessReview, error) {
	if tenantID.Zero() || reviewID.Zero() || actorID.Zero() ||
		(status != ReviewStatusCompleted && status != ReviewStatusCancelled) {
		return AccessReview{}, ErrInvalidReview
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	var updated AccessReview
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := repo.getReview(ctx, tenantID, reviewID)
		if err != nil {
			return err
		}
		if row.Status != ReviewStatusOpen {
			return ErrReviewStateConflict
		}
		if !row.DueAt.After(now) {
			return ErrReviewExpired
		}
		if status == ReviewStatusCompleted {
			var pending int64
			err := repo.writer().WithContext(ctx).Table(repo.tables(repo.writer()).AccessReviewItems.TableName()).
				Where("tenant_id = ? AND review_id = ? AND decision = ?", tenantID, reviewID, ReviewDecisionPending).
				Count(&pending).Error
			if err != nil {
				return fmt.Errorf("count pending access review items: %w", err)
			}
			if pending > 0 {
				return ErrReviewIncomplete
			}
		}
		values := map[string]any{"status": status, "updated_at": now}
		if status == ReviewStatusCompleted {
			values["completed_at"] = now
		} else {
			values["completed_at"] = nil
		}
		result := repo.writer().WithContext(ctx).Table(repo.tables(repo.writer()).AccessReviews.TableName()).
			Where("tenant_id = ? AND id = ? AND status = ?", tenantID, reviewID, ReviewStatusOpen).Updates(values)
		if result.Error != nil {
			return fmt.Errorf("finish access review: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrReviewStateConflict
		}
		row.Status, row.UpdatedAt = status, now
		if status == ReviewStatusCompleted {
			row.CompletedAt = timePointer(now)
		} else {
			row.CompletedAt = nil
		}
		items, err := repo.listReviewItems(ctx, tenantID, reviewID)
		if err != nil {
			return err
		}
		updated = reviewFromRows(row, items, now)
		event := audit.NewDomainEvent(ctx, tenantID, "access_review_"+status, "access_review", reviewID)
		event.Detail["actor_id"] = actorID
		return s.audit(ctx, repo.writer(), event)
	})
	if err != nil {
		return AccessReview{}, fmt.Errorf("finish access review: %w", err)
	}
	return updated, nil
}

func (r *Repository) listReviewItems(ctx context.Context, tenantID, reviewID guid.ID) ([]model.AccessReviewItem, error) {
	rows := make([]model.AccessReviewItem, 0)
	err := r.writer().WithContext(ctx).Table(r.tables(r.writer()).AccessReviewItems.TableName()).
		Where("tenant_id = ? AND review_id = ?", tenantID, reviewID).
		Order("principal_name ASC, principal_id ASC, role_name ASC, role_id ASC, grant_type ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list access review items: %w", err)
	}
	return rows, nil
}

// reviewCountRow is one (review, decision) bucket: the review's id, the
// decision value and how many items carry it. reviewCounts folds these into
// accessReviewCountRow totals instead of asking SQL for conditional sums.
type reviewCountRow struct {
	ReviewID guid.ID `gorm:"column:review_id"`
	Decision string  `gorm:"column:decision"`
	N        int     `gorm:"column:n"`
}

func (r *Repository) reviewCounts(ctx context.Context, tenantID guid.ID) (map[guid.ID]accessReviewCountRow, error) {
	items := r.tables(r.writer()).AccessReviewItems
	rows := make([]reviewCountRow, 0)
	if err := items.WithContext(ctx).
		Where(items.TenantID.Eq(tenantID)).
		Select(items.ReviewID, items.Decision, items.ReviewID.Count().As("n")).
		Group(items.ReviewID, items.Decision).
		Scan(&rows); err != nil {
		return nil, fmt.Errorf("count access review items: %w", err)
	}
	result := make(map[guid.ID]accessReviewCountRow, len(rows))
	for _, row := range rows {
		cur := result[row.ReviewID]
		cur.Total += row.N
		switch row.Decision {
		case ReviewDecisionPending:
			cur.Pending += row.N
		case ReviewDecisionKeep:
			cur.Kept += row.N
		case ReviewDecisionRevoke:
			cur.Revoked += row.N
		}
		result[row.ReviewID] = cur
	}
	return result, nil
}

func (r *Repository) removeRelationship(ctx context.Context, tenantID, principalID, grantID guid.ID) (bool, error) {
	relationships := r.foreign.access.Relationships.String()
	resourceRelationships := r.foreign.access.ResourceRelationships.String()
	result := r.writer().WithContext(ctx).Table(relationships).
		Where("tenant_id = ? AND subject_type = ? AND subject_id = ? AND resource_id = ?", tenantID, "principal", principalID, grantID).
		Delete(&struct{}{})
	if result.Error != nil {
		return false, fmt.Errorf("remove reviewed graph relationship: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return true, nil
	}
	result = r.writer().WithContext(ctx).Table(resourceRelationships).
		Where("tenant_id = ? AND subject_type = ? AND subject_id = ? AND entity_id = ?", tenantID, "principal", principalID, grantID).
		Delete(&struct{}{})
	if result.Error != nil {
		return false, fmt.Errorf("remove reviewed resource relationship: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func reviewFromRows(row model.AccessReview, rows []model.AccessReviewItem, now time.Time) AccessReview {
	status := row.Status
	if status == ReviewStatusOpen && !row.DueAt.After(now) {
		status = ReviewStatusExpired
	}
	review := AccessReview{
		ID: row.ID, TenantID: row.TenantID, Name: row.Name, OwnerID: row.OwnerID, Status: status,
		DueAt: row.DueAt.UTC(), CompletedAt: utcPointer(row.CompletedAt), CreatedAt: row.CreatedAt.UTC(),
		UpdatedAt: row.UpdatedAt.UTC(), Items: make([]AccessReviewItem, 0, len(rows)),
	}
	for _, row := range rows {
		item := reviewItemFromRow(row)
		review.Items = append(review.Items, item)
		review.Total++
		switch item.Decision {
		case ReviewDecisionPending:
			review.Pending++
		case ReviewDecisionKeep:
			review.Kept++
		case ReviewDecisionRevoke:
			review.Revoked++
		}
	}
	return review
}

func reviewItemFromRow(row model.AccessReviewItem) AccessReviewItem {
	return AccessReviewItem{
		ID: row.ID, ReviewID: row.ReviewID, PrincipalID: row.PrincipalID, PrincipalName: row.PrincipalName,
		RoleID: row.RoleID, RoleName: row.RoleName, GrantType: row.GrantType, GrantID: optionalID(row.GrantID),
		GrantCreatedAt: row.GrantCreatedAt.UTC(), GrantExpiresAt: utcPointer(row.GrantExpiresAt),
		Decision: row.Decision, DecidedBy: optionalID(row.DecidedBy), DecisionNote: row.DecisionNote,
		DecidedAt: utcPointer(row.DecidedAt),
	}
}
