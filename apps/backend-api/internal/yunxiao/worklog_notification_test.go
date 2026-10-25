package yunxiao

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorklogApprovalCountUsesIndependentCurrentAuthorityAndCutoff(t *testing.T) {
	m, db, ctx := workHarness(t)
	var row WorklogView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, workInput())
		if err != nil {
			return err
		}
		_, err = m.worklogStateOn(ctx, tx, 1, 99, row.ID, 1, "SUBMITTED", "")
		return err
	}))
	read := func(actor guid.ID, cutoff time.Time) int {
		var n int
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			n, err = m.worklogApprovalCountOn(t.Context(), tx, 1, actor, cutoff)
			return err
		}))
		return n
	}
	cutoff := m.now().Add(time.Minute)
	require.Zero(t, read(authn.PrincipalIDFromContext(ctx), cutoff))
	require.Equal(t, 1, read(12, cutoff))
	require.Zero(t, read(12, m.now().Add(-time.Hour)))
	m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return delivery.ErrWorkForbidden }}
	require.Zero(t, read(12, cutoff))
}
