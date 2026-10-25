package planning

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCollaborationInboxPagesAuthorizedSourcesInCanonicalOrder(t *testing.T) {
	h := setup(t)
	created := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	var failure error
	h.m.collaborationInbox = func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, kind string) ([]clientwork.CollaborationInboxItem, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.NotNil(t, tx)
		return []clientwork.CollaborationInboxItem{{ID: 9, ProjectID: 3, CreatedAt: created.Add(time.Hour)}, {ID: 8, ProjectID: 3, CreatedAt: created}}, failure
	}
	for _, kind := range []string{"DISPATCH_PROPOSAL", "EXCEPTION_DECISION", "PREVIEW_SHARE_APPROVAL"} {
		req := governance.InboxRequest{TenantID: 1, PrincipalID: 2, ProjectID: 3, Kind: kind, Limit: 1}
		rows, err := h.m.readCollaborationInbox(claims(t, 2), req)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, guid.ID(8), rows[0].ID)
		require.NotEqual(t, "project", rows[0].ObjectType)
		req.After = &rows[0].InboxKey
		rows, err = h.m.readCollaborationInbox(claims(t, 2), req)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, guid.ID(9), rows[0].ID)
	}
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 2, ProjectID: 3, Kind: "DISPATCH_PROPOSAL", Limit: 1}
	_, err := h.m.readCollaborationInbox(claims(t, 3), req)
	require.ErrorIs(t, err, ErrForbidden)
	failure = errors.New("source failure")
	_, err = h.m.readCollaborationInbox(claims(t, 2), req)
	require.ErrorIs(t, err, failure)
	failure = clientwork.ErrUnavailable
	_, err = h.m.readCollaborationInbox(claims(t, 2), req)
	require.ErrorIs(t, err, governance.ErrUnavailable)
}
