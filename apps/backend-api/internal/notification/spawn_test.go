package notification

import (
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/i18n"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestSpawnProjectionRejectsUnavailableSourceAndInvalidThreshold(t *testing.T) {
	for _, mode := range []string{"identity", "missing", "source", "type", "threshold", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			fact := governance.SpawnNotificationFact{GrantID: 91, GrantorID: 11, State: "ACTIVE", Cap: 100, Spent: 80}
			f.m.spawnFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (governance.SpawnNotificationFact, bool, error) {
				if mode == "source" {
					return fact, false, ErrUnavailable
				}
				return fact, true, nil
			}
			e := events.Envelope{EventID: 1001, TenantID: 7, EventType: "spawn_grant.budget_threshold", Subject: events.Subject{ID: 91}}
			switch mode {
			case "identity":
				e.Subject.ID = 0
			case "missing":
				f.m.spawnFacts = nil
			case "type":
				e.EventType = "spawn_grant.changed"
			case "threshold":
				fact.Spent = 79
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.consumeSpawnEventOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	f.m.spawnFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (governance.SpawnNotificationFact, bool, error) {
		return governance.SpawnNotificationFact{}, true, nil
	}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`, `{"subject_id":"91","pct":"99"}`} {
		yes, err := f.m.spawnAudienceOn(t.Context(), f.db, &model.Notification{TenantID: 7, Type: "governance.spawn_grant_threshold", Params: raw}, 11)
		require.False(t, yes)
		require.ErrorIs(t, err, ErrInvalid)
	}
}

func TestSpawnThresholdUsesCurrentCanonicalTenantOwnerAuthority(t *testing.T) {
	f := newFixture(t)
	f.m.spawnFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (governance.SpawnNotificationFact, bool, error) {
		return governance.SpawnNotificationFact{GrantID: 91, GrantorID: 11, State: "ACTIVE", Cap: 100, Spent: 80}, true, nil
	}
	owner := false
	f.m.explain = func(_ context.Context, _ *gorm.DB, request core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "project_takeover", request.Permission)
		require.Equal(t, guid.ID(7), request.TenantID)
		require.Zero(t, request.EntityID, "team-wide grants require tenant authority, not an arbitrary project grant")
		require.Equal(t, guid.ID(12), request.PrincipalID)
		return authz.Explanation{Allowed: owner}, nil
	}
	row := &model.Notification{TenantID: 7, Type: "governance.spawn_grant_threshold", Params: `{"subject_id":"91","pct":"80"}`}
	for _, allowed := range []bool{false, true, false} {
		owner = allowed
		yes, err := f.m.spawnAudienceOn(t.Context(), f.db, row, 12)
		require.NoError(t, err)
		require.Equal(t, allowed, yes, "owner grant and revocation must both be current")
	}
}

func TestSpawnThresholdOwnersSeparateCapsAndPendingRequestGrouping(t *testing.T) {
	f := newFixture(t)
	at := f.m.now()
	state := "ACTIVE"
	request := true
	spent := int64(800000)
	f.m.spawnFacts = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID, kind string) (governance.SpawnNotificationFact, bool, error) {
		s := state
		if kind == "request" {
			s = "REQUESTED"
		}
		return governance.SpawnNotificationFact{GrantID: 91, GrantorID: 11, State: s, Cap: 1000000, Spent: spent, EndsAt: at.Add(72 * time.Hour), ProjectIDs: []guid.ID{81}}, tenant == 7 && (kind != "request" || request), nil
	}
	e := events.Envelope{EventID: 1001, TenantID: 7, EventType: "spawn_grant.budget_threshold", Subject: events.Subject{ID: 91, Kind: "spawn_grant"}, Data: []byte(`{}`)}
	consume := func() {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.consumeSpawnEventOn(t.Context(), tx, e) }))
	}
	consume()
	consume()
	q := query.BindNotifications(f.db, f.m.names)
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	spent = 1000000
	state = "EXHAUSTED"
	e.EventType = "spawn_grant.exhausted"
	e.EventID++
	consume()
	consume()
	n, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 4, n)
	state = "ACTIVE"
	spent = 0
	e.EventType = "spawn_grant.granted"
	e.EventID++
	consume()
	queue := f.m.queue.(*fixtureQueue)
	require.Equal(t, at, queue.requests[len(queue.requests)-1].RunAt)
	raw, err := json.Marshal(sourceReminder{Kind: "spawn", SourceID: 91, Ordinal: 1})
	require.NoError(t, err)
	require.NoError(t, f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: raw}))
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("governance.spawn_grant_expiring")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	e.EventType = "governance.spawn_request_changed"
	e.Subject.Kind = "spawn_request"
	e.Subject.ID = 95
	consume()
	consume()
	e.Subject.ID = 96
	consume()
	rows, err = q.WithContext(t.Context()).Where(q.Type.Eq("agent.spawn_requested")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, 2, row.MergeCount)
		for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
			_, err := view(i18n.WithLocale(t.Context(), locale), row)
			require.NoError(t, err)
		}
	}
	request = false
	for _, row := range rows {
		yes, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.False(t, yes)
	}
}
