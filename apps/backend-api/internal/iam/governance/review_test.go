package governance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coremodel "7link.com/platform/internal/iam/core/model"
	governancemodel "7link.com/platform/internal/iam/governance/model"
	organizationmodel "7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedReviewableGrants(t *testing.T, h *governanceHarness) {
	t.Helper()
	h.seedTenant(t, 1)
	for id, name := range map[guid.ID]string{10: "Owner", 11: "Subject", 12: "Reviewer"} {
		h.seedMember(t, 1, id, name)
	}
	h.seedRole(t, 1, 20, "Operator")
	require.NoError(t, h.db.Table(h.table("iam_role_members")).Create(&coremodel.RoleMember{
		TenantID: 1, RoleID: 20, PrincipalID: 11, CreatedAt: governanceTestNow.Add(-7 * time.Hour),
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_temporary_role_grants")).Create(&coremodel.TemporaryRoleGrant{
		TenantID: 1, ID: 21, RoleID: 20, PrincipalID: 11, SourceType: "access_request", SourceID: 21,
		StartsAt: governanceTestNow.Add(-time.Hour), EndsAt: governanceTestNow.Add(time.Hour), CreatedBy: 12,
		CreatedAt: governanceTestNow.Add(-time.Hour),
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_groups")).Create(&organizationmodel.Group{
		TenantID: 1, ID: 30, Name: "Static", NameKey: "static", GroupType: "static", RuleJSON: "",
		Status: "active", Version: 1, CreatedAt: governanceTestNow.Add(-6 * time.Hour), UpdatedAt: governanceTestNow,
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_group_members")).Create(&organizationmodel.GroupMember{
		TenantID: 1, GroupID: 30, PrincipalID: 11, CreatedAt: governanceTestNow.Add(-5 * time.Hour), UpdatedAt: governanceTestNow,
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_group_role_bindings")).Create(&organizationmodel.GroupRoleBinding{
		TenantID: 1, GroupID: 30, RoleID: 20, CreatedAt: governanceTestNow.Add(-5 * time.Hour),
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_positions")).Create(&organizationmodel.Position{
		TenantID: 1, ID: 40, Code: "operator", Name: "Operator", Status: "active", Version: 1,
		CreatedAt: governanceTestNow.Add(-4 * time.Hour), UpdatedAt: governanceTestNow,
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_position_members")).Create(&organizationmodel.PositionMember{
		TenantID: 1, PositionID: 40, PrincipalID: 11, CreatedAt: governanceTestNow.Add(-4 * time.Hour), UpdatedAt: governanceTestNow,
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_position_role_bindings")).Create(&organizationmodel.PositionRoleBinding{
		TenantID: 1, PositionID: 40, RoleID: 20, CreatedAt: governanceTestNow.Add(-4 * time.Hour),
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_entities")).Create(&coremodel.Entity{
		TenantID: 1, ID: 50, Type: "folder", Name: "Root", NameKey: "root", Status: "active", Metadata: "{}",
		CreatedAt: governanceTestNow.Add(-3 * time.Hour), UpdatedAt: governanceTestNow,
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_role_bindings")).Create(&coremodel.RoleBinding{
		TenantID: 1, RoleID: 20, PrincipalID: 11, ScopeType: "entity", ScopeID: 50, Effect: "allow",
		CreatedAt: governanceTestNow.Add(-3 * time.Hour),
	}).Error)
	rule := `{"version":1,"match":"all","conditions":[{"field":"member.subject","operator":"in","values":["11"]}]}`
	require.NoError(t, h.db.Table(h.table("iam_groups")).Create(&organizationmodel.Group{
		TenantID: 1, ID: 60, Name: "Dynamic", NameKey: "dynamic", GroupType: "dynamic", RuleJSON: rule,
		Status: "active", Version: 1, CreatedAt: governanceTestNow.Add(-2 * time.Hour), UpdatedAt: governanceTestNow,
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_group_role_bindings")).Create(&organizationmodel.GroupRoleBinding{
		TenantID: 1, GroupID: 60, RoleID: 20, CreatedAt: governanceTestNow.Add(-2 * time.Hour),
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_relationships")).Create(&coremodel.Relationship{
		TenantID: 1, SubjectType: "principal", SubjectID: 11, Relation: "viewer", ResourceType: "folder", ResourceID: 50,
		ConditionJSON: "", CreatedAt: governanceTestNow.Add(-time.Hour),
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_resource_relationships")).Create(&coremodel.ResourceRelationship{
		TenantID: 1, EntityID: 50, SubjectType: "principal", SubjectID: 11, Relation: "editor",
		ResourceType: "document", ResourceID: 70, ConditionJSON: "", CreatedAt: governanceTestNow.Add(-30 * time.Minute),
	}).Error)
}

func TestAccessReviewSnapshotsAllGrantTypesAndCompletes(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	seedReviewableGrants(t, h)
	h.seedTenant(t, 2)
	h.seedMember(t, 2, 10, "Foreign")

	review, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Quarterly access review", DueAt: governanceTestNow.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, ReviewStatusOpen, review.Status)
	assert.Equal(t, 8, review.Total)
	assert.Equal(t, 8, review.Pending)
	types := map[string]int{}
	for _, item := range review.Items {
		types[item.GrantType]++
		assert.False(t, item.GrantCreatedAt.IsZero())
	}
	assert.Equal(t, map[string]int{
		ReviewGrantPermanent: 1, ReviewGrantTemporary: 1, ReviewGrantGroup: 1,
		ReviewGrantPosition: 1, ReviewGrantEntity: 1, ReviewGrantDynamicGroup: 1,
		ReviewGrantRelationship: 2,
	}, types)
	_, err = h.service.GetReview(t.Context(), 2, review.ID)
	assert.ErrorIs(t, err, ErrReviewNotFound)
	foreign, err := h.service.ListReviews(t.Context(), 2)
	require.NoError(t, err)
	assert.Empty(t, foreign)
	_, err = h.service.DecideReviewItem(t.Context(), 2, review.ID, review.Items[0].ID, 12, ReviewDecisionKeep, "cross tenant")
	assert.ErrorIs(t, err, ErrReviewNotFound)

	_, err = h.service.CompleteReview(t.Context(), 1, review.ID, 12)
	assert.ErrorIs(t, err, ErrReviewIncomplete)
	for _, item := range review.Items {
		decision := ReviewDecisionKeep
		if item.GrantType == ReviewGrantTemporary {
			decision = ReviewDecisionRevoke
		}
		_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, item.ID, 12, decision, "reviewed")
		require.NoError(t, err)
	}
	assert.Zero(t, h.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ?", 1, 21))
	completed, err := h.service.CompleteReview(t.Context(), 1, review.ID, 12)
	require.NoError(t, err)
	assert.Equal(t, ReviewStatusCompleted, completed.Status)
	assert.NotNil(t, completed.CompletedAt)
	assert.Zero(t, completed.Pending)
	assert.Equal(t, 7, completed.Kept)
	assert.Equal(t, 1, completed.Revoked)

	listed, err := h.service.ListReviews(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, 8, listed[0].Total)
	assert.Empty(t, listed[0].Items)
	_, err = h.service.CancelReview(t.Context(), 1, review.ID, 12)
	assert.ErrorIs(t, err, ErrReviewStateConflict)
}

func TestAccessReviewRejectsSelfDynamicAndLastAdministratorDecisions(t *testing.T) {
	h := newGovernanceHarness(t, "")
	seedReviewableGrants(t, h)
	review, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Privileged access review", DueAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	var permanent, dynamic AccessReviewItem
	for _, item := range review.Items {
		switch item.GrantType {
		case ReviewGrantPermanent:
			permanent = item
		case ReviewGrantDynamicGroup:
			dynamic = item
		}
	}
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, permanent.ID, 11, ReviewDecisionKeep, "self")
	assert.ErrorIs(t, err, ErrReviewSelfDecision)
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, dynamic.ID, 12, ReviewDecisionRevoke, "derived")
	assert.ErrorIs(t, err, ErrReviewDynamicDerived)
	h.mu.Lock()
	h.lastAdminFail = true
	h.mu.Unlock()
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, permanent.ID, 12, ReviewDecisionRevoke, "remove")
	assert.ErrorIs(t, err, ErrReviewLastAdministrator)
	stored, err := h.service.GetReview(t.Context(), 1, review.ID)
	require.NoError(t, err)
	for _, item := range stored.Items {
		if item.ID == permanent.ID || item.ID == dynamic.ID {
			assert.Equal(t, ReviewDecisionPending, item.Decision)
		}
	}
	assert.Equal(t, int64(1), h.count(t, "iam_role_members", "tenant_id = ? AND role_id = ? AND principal_id = ?", 1, 20, 11))
}

func TestAccessReviewAuditFailureRollsBackRevocation(t *testing.T) {
	h := newGovernanceHarness(t, "")
	seedReviewableGrants(t, h)
	review, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Rollback access review", DueAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	var temporary AccessReviewItem
	for _, item := range review.Items {
		if item.GrantType == ReviewGrantTemporary {
			temporary = item
		}
	}
	h.mu.Lock()
	h.failEvent = "access_review_item_revoked"
	h.mu.Unlock()
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, temporary.ID, 12, ReviewDecisionRevoke, "remove")
	assert.ErrorContains(t, err, "audit unavailable")
	assert.Equal(t, int64(1), h.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ?", 1, 21))
	var stored governancemodel.AccessReviewItem
	require.NoError(t, h.db.Table(h.table("iam_access_review_items")).Where("tenant_id = ? AND review_id = ? AND id = ?", 1, review.ID, temporary.ID).Take(&stored).Error)
	assert.Equal(t, ReviewDecisionPending, stored.Decision)
}

func TestAccessReviewCancelAndDerivedExpiry(t *testing.T) {
	h := newGovernanceHarness(t, "")
	seedReviewableGrants(t, h)
	for _, input := range []CreateAccessReview{
		{},
		{Name: "x", DueAt: governanceTestNow.Add(time.Hour)},
		{Name: "valid", DueAt: governanceTestNow},
		{Name: "valid", DueAt: governanceTestNow.Add(maxReviewDuration + time.Hour)},
	} {
		_, err := h.service.CreateReview(t.Context(), 1, 10, input)
		assert.ErrorIs(t, err, ErrInvalidReview)
	}
	review, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Cancellation review", DueAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	canceled, err := h.service.CancelReview(t.Context(), 1, review.ID, 12)
	require.NoError(t, err)
	assert.Equal(t, ReviewStatusCancelled, canceled.Status)
	assert.Nil(t, canceled.CompletedAt)

	expiring, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Expiring review", DueAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	h.service.now = func() time.Time { return governanceTestNow.Add(2 * time.Hour) }
	got, err := h.service.GetReview(t.Context(), 1, expiring.ID)
	require.NoError(t, err)
	assert.Equal(t, ReviewStatusExpired, got.Status)
	_, err = h.service.DecideReviewItem(t.Context(), 1, expiring.ID, expiring.Items[0].ID, 12, ReviewDecisionKeep, "late")
	assert.ErrorIs(t, err, ErrReviewExpired)
	_, err = h.service.CancelReview(t.Context(), 1, expiring.ID, 12)
	assert.ErrorIs(t, err, ErrReviewExpired)
}

func TestAccessReviewRevokesEveryMutableGrantClass(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	seedReviewableGrants(t, h)
	review, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Revoke access review", DueAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	for _, item := range review.Items {
		if item.GrantType == ReviewGrantDynamicGroup || item.GrantType == ReviewGrantTemporary {
			continue
		}
		_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, item.ID, 12, ReviewDecisionRevoke, "remove")
		require.NoError(t, err, item.GrantType)
	}
	assert.Zero(t, h.count(t, "iam_role_members", "tenant_id = ?", 1))
	assert.Zero(t, h.count(t, "iam_group_members", "tenant_id = ?", 1))
	assert.Zero(t, h.count(t, "iam_position_members", "tenant_id = ?", 1))
	assert.Zero(t, h.count(t, "iam_role_bindings", "tenant_id = ?", 1))
	assert.Zero(t, h.count(t, "iam_relationships", "tenant_id = ?", 1))
	assert.Zero(t, h.count(t, "iam_resource_relationships", "tenant_id = ?", 1))
}

func TestAccessReviewConcurrentFinishHasOneTerminalWinner(t *testing.T) {
	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Owner")
	h.seedMember(t, 1, 11, "Subject")
	h.seedMember(t, 1, 12, "Reviewer")
	h.seedRole(t, 1, 20, "Operator")
	require.NoError(t, h.db.Table(h.table("iam_role_members")).Create(&coremodel.RoleMember{
		TenantID: 1, RoleID: 20, PrincipalID: 11, CreatedAt: governanceTestNow.Add(-time.Hour),
	}).Error)
	review, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Concurrent finish", DueAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, review.Items[0].ID, 12, ReviewDecisionKeep, "keep")
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, status := range []string{ReviewStatusCompleted, ReviewStatusCancelled} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if status == ReviewStatusCompleted {
				_, err := h.service.CompleteReview(context.Background(), 1, review.ID, 12)
				results <- err
				return
			}
			_, err := h.service.CancelReview(context.Background(), 1, review.ID, 12)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	assert.Equal(t, 1, successes)
	stored, err := h.service.GetReview(t.Context(), 1, review.ID)
	require.NoError(t, err)
	assert.Contains(t, []string{ReviewStatusCompleted, ReviewStatusCancelled}, stored.Status)
}

func TestAccessReviewCreationFailuresRemainAtomic(t *testing.T) {
	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Owner")
	input := CreateAccessReview{Name: "Quarterly review", DueAt: governanceTestNow.Add(time.Hour)}

	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).
		Where("tenant_id = ? AND principal_id = ?", 1, 10).Update("status", "disabled").Error)
	_, err := h.service.CreateReview(t.Context(), 1, 10, input)
	assert.ErrorIs(t, err, ErrRequesterInactive)
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).
		Where("tenant_id = ? AND principal_id = ?", 1, 10).Update("status", "active").Error)
	_, err = h.service.CreateReview(t.Context(), 1, 10, input)
	assert.ErrorIs(t, err, ErrReviewEmpty)

	h.seedMember(t, 1, 11, "Subject")
	h.seedRole(t, 1, 20, "Operator")
	require.NoError(t, h.db.Table(h.table("iam_role_members")).Create(&coremodel.RoleMember{
		TenantID: 1, RoleID: 20, PrincipalID: 11, CreatedAt: governanceTestNow,
	}).Error)
	h.service.repo.ids = failingGovernanceIDs{}
	_, err = h.service.CreateReview(t.Context(), 1, 10, input)
	assert.ErrorContains(t, err, "ID source unavailable")

	calls := 0
	h.service.repo.ids = governanceIDSourceFunc(func() (int64, error) {
		calls++
		if calls == 1 {
			return 2000, nil
		}
		return 0, errors.New("item ID unavailable")
	})
	_, err = h.service.CreateReview(t.Context(), 1, 10, input)
	assert.ErrorContains(t, err, "item ID unavailable")
	assert.Zero(t, h.count(t, "iam_access_reviews", "tenant_id = ?", 1))
	assert.Zero(t, h.count(t, "iam_access_review_items", "tenant_id = ?", 1))

	h.service.repo.ids = newGovernanceTestIDs(3000)
	h.mu.Lock()
	h.failEvent = "access_review_created"
	h.mu.Unlock()
	_, err = h.service.CreateReview(t.Context(), 1, 10, input)
	assert.ErrorContains(t, err, "audit unavailable")
	assert.Zero(t, h.count(t, "iam_access_reviews", "tenant_id = ?", 1))
	assert.Zero(t, h.count(t, "iam_access_review_items", "tenant_id = ?", 1))
}

func TestAccessReviewDecisionValidationAndSnapshotFailures(t *testing.T) {
	h := newGovernanceHarness(t, "")
	seedReviewableGrants(t, h)
	review, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Decision validation", DueAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)

	_, err = h.service.DecideReviewItem(t.Context(), 0, review.ID, review.Items[0].ID, 12, ReviewDecisionKeep, "invalid")
	assert.ErrorIs(t, err, ErrInvalidReview)
	_, err = h.service.DecideReviewItem(t.Context(), 1, 999, review.Items[0].ID, 12, ReviewDecisionKeep, "missing review")
	assert.ErrorIs(t, err, ErrReviewNotFound)
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, 999, 12, ReviewDecisionKeep, "missing item")
	assert.ErrorIs(t, err, ErrReviewItemNotFound)
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, review.Items[0].ID, 12, ReviewDecisionKeep, "keep")
	require.NoError(t, err)
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, review.Items[0].ID, 12, ReviewDecisionKeep, "again")
	assert.ErrorIs(t, err, ErrReviewStateConflict)
	_, err = h.service.CancelReview(t.Context(), 1, review.ID, 12)
	require.NoError(t, err)
	_, err = h.service.DecideReviewItem(t.Context(), 1, review.ID, review.Items[1].ID, 12, ReviewDecisionKeep, "closed")
	assert.ErrorIs(t, err, ErrReviewStateConflict)
	_, err = h.service.finishReview(t.Context(), 0, review.ID, 12, ReviewStatusCompleted)
	assert.ErrorIs(t, err, ErrInvalidReview)
	_, err = h.service.CompleteReview(t.Context(), 1, 999, 12)
	assert.ErrorIs(t, err, ErrReviewNotFound)

	for _, item := range []governancemodel.AccessReviewItem{
		{GrantType: ReviewGrantTemporary},
		{GrantType: ReviewGrantGroup},
		{GrantType: ReviewGrantPosition},
		{GrantType: ReviewGrantEntity},
		{GrantType: ReviewGrantRelationship},
		{GrantType: "unknown"},
	} {
		changed, err := h.service.revokeReviewedGrant(t.Context(), h.service.repo, item)
		assert.False(t, changed)
		assert.ErrorIs(t, err, ErrInvalidReview, item.GrantType)
	}
}

func TestAccessReviewReadFailuresAreReported(t *testing.T) {
	var unavailableService *Service
	_, err := unavailableService.ListReviews(t.Context(), 1)
	assert.ErrorIs(t, err, ErrInvalidReview)
	_, err = unavailableService.GetReview(t.Context(), 1, 1)
	assert.ErrorIs(t, err, ErrInvalidReview)

	t.Run("reviews", func(t *testing.T) {
		h := newGovernanceHarness(t, "")
		require.NoError(t, h.db.Migrator().DropTable(h.table("iam_access_reviews")))
		_, err := h.service.ListReviews(t.Context(), 1)
		assert.ErrorContains(t, err, "list access reviews")
	})

	t.Run("review items", func(t *testing.T) {
		h := newGovernanceHarness(t, "")
		require.NoError(t, h.db.Table(h.table("iam_access_reviews")).Create(&governancemodel.AccessReview{
			TenantID: 1, ID: 1, Name: "Stored review", OwnerID: 10, Status: ReviewStatusOpen,
			DueAt: governanceTestNow.Add(time.Hour), CreatedAt: governanceTestNow, UpdatedAt: governanceTestNow,
		}).Error)
		require.NoError(t, h.db.Migrator().DropTable(h.table("iam_access_review_items")))
		_, err := h.service.GetReview(t.Context(), 1, 1)
		assert.ErrorContains(t, err, "list access review items")
	})
}
