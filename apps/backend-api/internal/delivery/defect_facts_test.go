package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestDefectMergeCapabilityBindsExactlyOnce(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideCodeMerge(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[DefectMergeConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrInvalidArgument)
	h, _, _ := deploymentFixture(t)
	require.NoError(t, c.Bind(h.m.defectMerge))
	require.ErrorIs(t, c.Bind(h.m.defectMerge), ErrInvalidArgument)
}
func TestRuntimeDefectCannotAcceptWithoutTrustedDeployment(t *testing.T) {
	h, ctx, in := deploymentFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		w := query.BindWorkItems(tx, h.m.service.names)
		work, e := w.WithContext(ctx).Where(w.ID.Eq(h.work)).First()
		require.NoError(t, e)
		result, e := h.m.resultOn(ctx, tx, 1, 99, h.work, in.ResultID)
		require.NoError(t, e)
		require.ErrorIs(t, h.m.defectDeploymentReadyOn(context.Background(), tx, work, result), ErrVerificationPending)
		_, e = h.m.recordDeploymentOn(ctx, tx, 1, 99, h.work, in)
		require.NoError(t, e)
		work, e = w.WithContext(ctx).Where(w.ID.Eq(h.work)).First()
		require.NoError(t, e)
		require.NoError(t, h.m.defectDeploymentReadyOn(ctx, tx, work, result))
		return nil
	}))
}
