package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestKnowledgeNotificationsUseSourceAudienceReplayAndLocale(t *testing.T) {
	f := newFixture(t)
	eligible := true
	f.m.knowledgeAudience = func(_ context.Context, _ *gorm.DB, tenant, source, recipient guid.ID, typ string) (guid.ID, bool, error) {
		return 81, eligible && recipient == 11, nil
	}
	for _, e := range []events.RecordedEvent{{ID: 1001, TenantID: 7, SubjectID: 91, Type: "knowledge.memory_reviewed", Data: []byte(`{"action":"APPROVED","reason":"private"}`)}, {ID: 1002, TenantID: 7, SubjectID: 92, Type: "knowledge.source_failed", Data: []byte(`{"state":"FAILED"}`)}} {
		for range 2 {
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
		}
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.NotContains(t, row.Params, "private")
		for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
			_, err := view(i18n.WithLocale(t.Context(), locale), row)
			require.NoError(t, err)
		}
		eligible = false
		yes, err := f.m.visibleOn(t.Context(), f.db, row, 11)
		require.NoError(t, err)
		require.False(t, yes)
	}
}

func TestKnowledgeProjectionFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.knowledgeAudience = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, bool, error) {
				if mode == "source" {
					return 0, false, ErrUnavailable
				}
				return 81, true, nil
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "knowledge_source", Type: "knowledge.source_failed", Data: []byte(`{}`)}
			switch mode {
			case "missing":
				f.m.knowledgeAudience = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectKnowledgeEventOn(t.Context(), tx, e, nil) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
