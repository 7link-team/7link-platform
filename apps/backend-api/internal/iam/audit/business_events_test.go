package audit

import (
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBusinessAuditProjectionVisibilityPagingAndPrivateFacts(t *testing.T) {
	h := newAuditHarness(t, "bt_")
	m := &Module{service: h.service}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 8})
	for i := 0; i < 205; i++ {
		tenant := guid.ID(1)
		d := map[string]any{"project_id": "99", "actor_kind": "AGENT", "actor_name": "Recorded actor", "authority_source": "POLICY:spawn_grant", "authority_label": "Recorded grant", "origin": "AGENT_SPAWNED", "spawn_grant_id": "9007199254740993", "supervisor_agent_id": "9007199254740995", "private": "never return"}
		if i%2 == 0 {
			d["project_id"] = "98"
		}
		if i == 204 {
			tenant = 2
		}
		_, err := h.service.Append(ctx, EventInput{TenantID: tenant, PrincipalID: 8, EventType: "fleet.agent_spawned", Outcome: "success", Detail: d, IPAddress: "private-ip", UserAgent: "private-agent"})
		require.NoError(t, err)
	}
	q := BusinessQuery{Filter: Filter{TenantID: 1, Module: "fleet", ActorKind: "AGENT", Offset: 1, Limit: 2}, AuthorityKind: "POLICY", AuthorityRef: "spawn_grant", Origin: "AGENT_SPAWNED", SpawnGrantID: 9007199254740993}
	var rows []BusinessEvent
	var count int64
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		rows, count, err = m.readBusinessOn(ctx, tx, q, func(e BusinessEvent) (bool, error) { return e.ProjectID == 99, nil })
		return err
	}))
	require.EqualValues(t, 102, count)
	require.Len(t, rows, 2)
	require.EqualValues(t, 202, rows[0].Sequence)
	require.Greater(t, rows[0].Sequence, rows[1].Sequence, "same timestamp ties use chain sequence")
	require.Equal(t, "Recorded actor", rows[0].Actor.Name)
	require.Equal(t, "Recorded grant", rows[0].Authority.Label)
	require.EqualValues(t, 9007199254740993, rows[0].SpawnGrantID)
	require.EqualValues(t, 9007199254740995, rows[0].SupervisorAgentID)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	for _, secret := range []string{"private", "ip_address", "user_agent", "event_hash", "detail"} {
		require.NotContains(t, string(encoded), secret)
	}
	q.AuthorityRef = "other"
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		rows, count, err = m.readBusinessOn(ctx, tx, q, func(BusinessEvent) (bool, error) { t.Fatal("filter before authority"); return false, nil })
		return err
	}))
	require.Empty(t, rows)
	require.Zero(t, count)
}

func TestBusinessAuditProjectionFailsClosedAndCapabilityContract(t *testing.T) {
	for _, detail := range []string{`{`, `{"project_id":"bad"}`, `{"project_id":0}`, `{"project_id":false}`} {
		_, ok := businessProjection(Event{Detail: json.RawMessage(detail)})
		require.False(t, ok, detail)
	}
	e, ok := businessProjection(Event{Detail: json.RawMessage(`{"project_id":9007199254740993,"actor_kind":"AGENT"}`)})
	require.True(t, ok)
	require.EqualValues(t, 9007199254740993, e.ProjectID)
	require.Empty(t, e.Actor.Name)
	require.Empty(t, e.Authority.Kind)
	require.Empty(t, e.Origin, "missing facts never inferred")
	legacy, ok := businessProjection(Event{EventType: "legacy_event", Detail: json.RawMessage(`{}`)})
	require.True(t, ok)
	require.Empty(t, legacy.Module, "a legacy event without a namespace has no recorded module")
	for _, q := range []BusinessQuery{
		{Filter: Filter{EventType: "FLEET.agent_spawned"}},
		{EventTypes: []string{"FLEET.agent_spawned"}},
		{Filter: Filter{TargetType: "AGENT"}},
		{Filter: Filter{Outcome: "SUCCESS"}},
	} {
		require.False(t, businessMatches(BusinessEvent{EventType: "fleet.agent_spawned", Target: BusinessTarget{Type: "agent"}, Outcome: "success"}, q), "a case-insensitive SQL superset must not broaden exact business filters")
	}
	h := newAuditHarness(t, "")
	m := &Module{service: h.service}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 8})
	q := BusinessQuery{Filter: Filter{TenantID: 1, Limit: 2}}
	allow := func(BusinessEvent) (bool, error) { return true, nil }
	_, _, err := m.readBusinessOn(ctx, h.db, q, allow)
	require.ErrorIs(t, err, ErrInvalidFilter)
	_, _, err = (&Module{}).readBusinessOn(ctx, h.db, q, allow)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = h.service.Append(ctx, EventInput{TenantID: 1, PrincipalID: 8, EventType: "fleet.test", Outcome: "success"})
	require.NoError(t, err)
	require.EqualError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.readBusinessOn(ctx, tx, q, func(BusinessEvent) (bool, error) { return false, errors.New("authority unavailable") })
		return err
	}), "authority unavailable")
	s := module.NewServices()
	require.NoError(t, m.provideBusinessEvents(s))
	require.NoError(t, s.Seal())
	reader, err := module.Resolve[BusinessReader](s)
	require.NoError(t, err)
	require.NotNil(t, reader)
}
