package iamintegration

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
)

func verifySystemAdministratorLockOrder(t *testing.T, f *iamTenantFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	guard, err := module.Resolve[core.AdministratorGuard](f.app.Modules().Services())
	require.NoError(t, err)
	revisions, err := core.NewRevisionStore(f.app.Runtime())
	require.NoError(t, err)
	var system struct{ ID guid.ID }
	require.NoError(t, f.db.Table(f.table("iam_tenants")).Select("id").Where("slug = ?", core.SystemRootSlug).Take(&system).Error)
	for _, subject := range []guid.ID{f.adminA.ID, f.adminB.ID} {
		var full struct{ PrincipalID guid.ID }
		require.NoError(t, f.db.Table(f.table("iam_platform_administrators")).Where("principal_id = ?", subject).Take(&full).Error)
	}
	first := f.db.WithContext(ctx).Begin()
	require.NoError(t, first.Error)
	defer first.Rollback()
	var principal struct{ ID guid.ID }
	require.NoError(t, first.Raw("SELECT id FROM "+f.table("iam_principals")+" WHERE id = ? FOR UPDATE", f.adminA.ID).Scan(&principal).Error)
	require.NoError(t, revisions.Lock(ctx, first, system.ID))
	second := f.db.WithContext(ctx).Begin()
	require.NoError(t, second.Error)
	defer second.Rollback()
	require.NoError(t, second.Raw("SELECT id FROM "+f.table("iam_principals")+" WHERE id = ? FOR UPDATE", f.adminB.ID).Scan(&principal).Error)
	secondResult := make(chan error, 1)
	go func() { secondResult <- revisions.Lock(ctx, second, system.ID) }()
	firstResult := make(chan error, 1)
	go func() {
		verify, err := guard.Protect(ctx, first, system.ID)
		if err == nil {
			err = verify()
		}
		firstResult <- err
	}()
	select {
	case err := <-firstResult:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("system administrator guard waited for another principal after acquiring the system fence")
	}
	require.NoError(t, first.Commit().Error)
	select {
	case err := <-secondResult:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("second administrator did not resume after system fence release")
	}
	require.NoError(t, second.Commit().Error)
}
