package core

import (
	"7link.com/platform/pkg/module"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestBindingRevocationAdvancesRevisionAndRollsBack(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	entity, e := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "project", Name: "Project"})
	require.NoError(t, e)
	role, e := h.service.CreateRole(t.Context(), 1, "Reviewer", "")
	require.NoError(t, e)
	_, _, e = h.service.PutEntityRoleBinding(t.Context(), 1, entity.ID, role.ID, 11, BindingAllow, time.Time{})
	require.NoError(t, e)
	m := &Module{service: h.service}
	s := module.NewServices()
	require.NoError(t, m.provideBindingRevoker(s))
	require.NoError(t, s.Seal())
	revoke, e := module.Resolve[EntityRoleBindingRevoker](s)
	require.NoError(t, e)
	before, e := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, e)
	abort := errors.New("later outbox failed")
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		changed, e := revoke(t.Context(), tx, 1, entity.ID, role.ID, 11)
		require.NoError(t, e)
		require.True(t, changed)
		return abort
	}), abort)
	after, e := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, e)
	require.Equal(t, before, after)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		changed, e := revoke(t.Context(), tx, 1, entity.ID, role.ID, 11)
		require.NoError(t, e)
		require.True(t, changed)
		return e
	}))
	after, e = h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, e)
	require.Greater(t, after, before)
	_, e = revoke(t.Context(), h.db, 1, entity.ID, role.ID, 11)
	require.Error(t, e)
}
