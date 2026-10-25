package notification

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExceptionProjectionUsesCurrentSourceAndHumanAudience(t *testing.T) {
	f := newFixture(t)
	state, exists := "RAISED", true
	var sourceErr error
	f.m.exceptionFacts = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (clientwork.ExceptionNotificationFact, bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(91), id)
		return clientwork.ExceptionNotificationFact{ProjectID: 9, State: state}, exists, sourceErr
	}
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.PrincipalID == 12 && r.Permission == "exception_decide"}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, Type: "clientwork.exception_raised", ActorID: 11, Data: json.RawMessage(`{"report":"private report","notify_principal_ids":[11]}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	require.Equal(t, guid.ID(12), row.RecipientPrincipalID)
	require.Equal(t, "exception.raised", row.Type)
	require.Equal(t, "/admin/7/entities/9/exceptions?collection=exceptions&id=91", row.LinkPath)
	require.NotContains(t, row.Params, "private")
	require.Equal(t, 1, row.MergeCount)
	require.Equal(t, "IMMEDIATE", defaultEmail(row.Type, Action))
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.PrincipalID == 12 && r.Permission == "project_takeover"}, nil
	}
	allowed, err := f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.True(t, allowed, "current owners remain in the source audience")
	f.m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, nil
	}
	allowed, err = f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, allowed, "revoking both current audience grants hides an existing notification")
	f.m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}
	state = "DECIDED"
	allowed, err = f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, allowed)
	state = "REPORTED"
	exists = false
	allowed, err = f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, allowed)
	exists = true
	sourceErr = errors.New("source failed")
	e.SubjectID = 91
	e.ID++
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }), sourceErr)
	f.m.exceptionFacts = nil
	_, err = f.m.exceptionAudienceOn(t.Context(), f.db, row, 12)
	require.ErrorIs(t, err, ErrUnavailable)
}
