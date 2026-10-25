package core

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestTemporaryGrantReaderFencesTenantPrincipalAndTransaction(t *testing.T) {
	h := newRepositoryHarness(t, "sl_")
	seedTenantAndPrincipal(t, h.db, "sl_", 1, 11)
	_, err := h.repository.PutMember(t.Context(), TenantMember{TenantID: 1, PrincipalID: 11, DisplayName: "Member", Email: "member@example.test", Status: MemberActive})
	require.NoError(t, err)
	role, err := h.repository.CreateRole(t.Context(), 1, "Temporary", "")
	require.NoError(t, err)
	start := repositoryTestTime.Add(-time.Hour)
	end := repositoryTestTime.Add(time.Hour)
	require.NoError(t, h.repository.GrantTemporaryRole(t.Context(), 1, 500, role.ID, 11, 99, start, end))
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("sl_")), Config{})
	services := module.NewServices()
	require.NoError(t, m.provideProjectMembership(services))
	require.NoError(t, services.Seal())
	reader, err := module.Resolve[TemporaryGrantReader](services)
	require.NoError(t, err)
	_, _, err = reader(t.Context(), h.db, 1, 11, 500)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		fact, found, e := reader(t.Context(), tx, 1, 11, 500)
		require.NoError(t, e)
		require.True(t, found)
		require.Equal(t, role.ID, fact.RoleID)
		require.Equal(t, start, fact.StartsAt.UTC())
		require.Equal(t, end, fact.EndsAt.UTC())
		require.Equal(t, "access_request", fact.SourceType)
		for _, pair := range [][2]guid.ID{{2, 11}, {1, 22}} {
			_, found, e = reader(t.Context(), tx, pair[0], pair[1], 500)
			require.NoError(t, e)
			require.False(t, found)
		}
		return nil
	}))
	_, err = h.repository.RevokeTemporaryRole(t.Context(), 1, 500)
	require.NoError(t, err)
}

func TestProjectMemberBindingsOwnerDeletePreservesDeniesAndAtomicRevision(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedAdditionalMember(t, 1, 12, MemberActive)
	h.service.reservedEntityTypes = map[string]struct{}{"project": {}}
	h.service.roots = func(context.Context, *gorm.DB, FileRootProvisionInput, time.Time) (guid.ID, error) { return 9001, nil }
	role, e := h.service.CreateRole(t.Context(), 1, "Project lead", "")
	require.NoError(t, e)
	m := &Module{service: h.service}
	var project Entity
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		project, e = m.entityProvisioner().CreateOn(t.Context(), tx, Entity{TenantID: 1, Type: "project", Name: "Owned"})
		return e
	}))
	require.NoError(t, h.db.Exec("CREATE TABLE member_audit (kind TEXT)").Error)
	h.service.writes.audit = func(ctx context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		return tx.Exec("INSERT INTO member_audit VALUES (?)", e.EventType).Error
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.entityRoleBindingStore().PutOn(t.Context(), tx, 1, project.ID, role.ID, 11, BindingAllow, time.Time{})
		return e
	}))
	before, e := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, e)
	abort := errors.New("outbox failure")
	e = h.db.Transaction(func(tx *gorm.DB) error {
		changed, e := m.deleteProjectMemberBindingOn(t.Context(), tx, 1, project.ID, role.ID, 11)
		require.NoError(t, e)
		require.True(t, changed)
		return abort
	})
	require.ErrorIs(t, e, abort)
	after, e := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, e)
	require.Equal(t, before, after)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		rows, e := m.projectMemberBindingsOn(t.Context(), tx, 1, project.ID)
		require.NoError(t, e)
		require.Len(t, rows, 1)
		changed, e := m.deleteProjectMemberBindingOn(t.Context(), tx, 1, project.ID, role.ID, 11)
		require.NoError(t, e)
		require.True(t, changed)
		return nil
	}))
	after, e = h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, e)
	require.Greater(t, after, before)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.entityRoleBindingStore().PutOn(t.Context(), tx, 1, project.ID, role.ID, 12, BindingDeny, time.Time{})
		return e
	}))
	e = h.db.Transaction(func(tx *gorm.DB) error {
		_, e := m.deleteProjectMemberBindingOn(t.Context(), tx, 1, project.ID, role.ID, 12)
		return e
	})
	require.ErrorIs(t, e, ErrPrivilegeEscalation)
	e = h.db.Transaction(func(tx *gorm.DB) error {
		_, e := m.deleteProjectMemberBindingOn(t.Context(), tx, 2, project.ID, role.ID, 12)
		return e
	})
	require.ErrorIs(t, e, ErrEntityNotFound)
	_, e = m.deleteProjectMemberBindingOn(t.Context(), h.db, 1, project.ID, role.ID, 12)
	require.ErrorIs(t, e, ErrInvalidArgument)
}
