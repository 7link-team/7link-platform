package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCreateEntityProvisionsFileRootInItsTransaction(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	called := 0
	h.service.roots = func(ctx context.Context, tx *gorm.DB, input FileRootProvisionInput, _ time.Time) (guid.ID, error) {
		called++
		require.Equal(t, "entity", input.Scope)
		require.Equal(t, guid.ID(1), input.TenantID)
		require.Zero(t, input.EntityID)
		var count int64
		require.NoError(t, tx.WithContext(ctx).Table("sl_iam_entities").Where("tenant_id = ? AND id = ?", input.TenantID, input.OwnerID).Count(&count).Error)
		require.Equal(t, int64(1), count, "the provisioner must see the uncommitted entity")
		return 9001, nil
	}
	created, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "store", Name: "Store"})
	require.NoError(t, err)
	require.Positive(t, created.ID)
	require.Equal(t, 1, called)
}

func TestCreateEntityRollsBackWhenFileRootProvisioningFails(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	want := errors.New("root unavailable")
	h.service.roots = func(context.Context, *gorm.DB, FileRootProvisionInput, time.Time) (guid.ID, error) {
		return 0, want
	}
	_, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "store", Name: "Store"})
	require.ErrorIs(t, err, want)
	entities, err := h.service.ListEntities(t.Context(), 1)
	require.NoError(t, err)
	require.Empty(t, entities)
}

func TestDeleteEntityRefusesRetainedFileContent(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	created, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "store", Name: "Store"})
	require.NoError(t, err)
	called := 0
	h.service.rootContent = func(ctx context.Context, tx *gorm.DB, input FileRootProvisionInput) (bool, error) {
		called++
		require.Equal(t, FileRootProvisionInput{Scope: "entity", TenantID: 1, OwnerID: created.ID}, input)
		var count int64
		require.NoError(t, tx.WithContext(ctx).Table("sl_iam_entities").Where("tenant_id = ? AND id = ?", 1, created.ID).Count(&count).Error)
		require.Equal(t, int64(1), count, "the guard must run before entity deletion")
		return true, nil
	}
	require.ErrorIs(t, h.service.DeleteEntity(t.Context(), 1, created.ID), ErrEntityHasFiles)
	stored, err := h.service.GetEntity(t.Context(), 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, stored.ID)
	require.Equal(t, 1, called)
}
