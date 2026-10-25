package yunxiao

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorklogInboxUsesCurrentApprovalAuthorityAndExcludesSelf(t *testing.T) {
	m, db, ctx := workHarness(t)
	var row WorklogView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, workInput())
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.worklogStateOn(ctx, tx, 1, 99, row.ID, 1, "SUBMITTED", "")
		return err
	}))
	actor := authn.PrincipalIDFromContext(ctx)
	req := governance.InboxRequest{TenantID: 1, PrincipalID: actor, ProjectID: 99, Kind: "WORKLOG_APPROVAL", Limit: 1}
	items, err := m.worklogInbox(ctx, req)
	require.NoError(t, err)
	require.Empty(t, items)
	other := guid.ID(12)
	require.NoError(t, db.Table(m.names.Worklogs.String()).Where("id = ?", row.ID).Update("principal_id", other).Error)
	items, err = m.worklogInbox(ctx, req)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, row.ID, items[0].ObjectID)
	req.After = &items[0].InboxKey
	items, err = m.worklogInbox(ctx, req)
	require.NoError(t, err)
	require.Empty(t, items)
	req.After = nil
	for _, denied := range []error{ErrForbidden, delivery.ErrWorkForbidden, delivery.ErrInvalidMember, delivery.ErrProjectNotFound, delivery.ErrWorkProjectReadOnly} {
		m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return denied }}
		items, err = m.worklogInbox(ctx, req)
		require.NoError(t, err)
		require.Empty(t, items, "current owner denial removes actionable source")
	}
	unavailable := errors.New("owner database unavailable")
	m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return unavailable }}
	_, err = m.worklogInbox(ctx, req)
	require.ErrorIs(t, err, unavailable, "unexpected source errors must remain visible")
	req.TenantID = 2
	_, err = m.worklogInbox(ctx, req)
	require.ErrorIs(t, err, governance.ErrForbidden)
}

func TestWorklogInboxFailsClosedOnCurrentIdentityAndStorageFailure(t *testing.T) {
	for _, mode := range []string{"missing", "lock", "identity_error", "inactive", "membership", "query"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			req := governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "WORKLOG_APPROVAL", Limit: 1}
			switch mode {
			case "missing":
				m.principals.Lock = nil
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "identity_error":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "membership":
				m.members = testMembers(false)
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("inbox_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Worklogs.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("inbox_storage")
			}
			items, e := m.worklogInbox(ctx, req)
			require.Error(t, e)
			require.Empty(t, items)
		})
	}
	m, _, ctx := workHarness(t)
	_, e := m.worklogInbox(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "UNKNOWN", Limit: 1})
	require.ErrorIs(t, e, governance.ErrInvalidArgument)
	_, e = (&Module{}).worklogInbox(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "WORKLOG_APPROVAL", Limit: 1})
	require.ErrorIs(t, e, governance.ErrUnavailable)
}
