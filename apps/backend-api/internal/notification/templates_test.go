package notification

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTemplatePublicationCurrentPinAudienceAndReplay(t *testing.T) {
	f := newFixture(t)
	pinned := true
	f.m.templatePublications = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) {
		if pinned {
			return []guid.ID{81}, nil
		}
		return nil, nil
	}
	f.m.projectAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10"}}}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 91, Type: "template.published", Data: []byte(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	n := len(rows)
	require.LessOrEqual(t, n, 2)
	pinned = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], rows[0].RecipientPrincipalID)
	require.NoError(t, err)
	require.False(t, yes)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, n, count)
}

func TestTemplatePublicationProjectionFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.templatePublications = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) {
				if mode == "source" {
					return nil, ErrUnavailable
				}
				return []guid.ID{81}, nil
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91}
			switch mode {
			case "missing":
				f.m.templatePublications = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectTemplatePublicationOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
