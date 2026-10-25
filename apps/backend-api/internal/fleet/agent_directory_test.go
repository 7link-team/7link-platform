package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestAgentDirectoryBoundedCurrentHumanAndNoPrivateConfiguration(t *testing.T) {
	m, db, ctx, _, _, _, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(ctx, tx, 7, in); return e }))
	foreign, e := query.BindAgents(db, m.names).WithContext(ctx).First()
	require.NoError(t, e)
	foreign.ID, foreign.TenantID, foreign.PrincipalID, foreign.Handle = 990, 8, 991, "foreign-agent"
	require.NoError(t, query.BindAgents(db, m.names).WithContext(ctx).Create(foreign))
	first, e := m.directoryHTTP(ctx, &AgentDirectoryInput{Limit: 1})
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	require.Equal(t, guid.ID(101), first.Body.Data.Items[0].PrincipalID)
	last, e := m.directoryHTTP(ctx, &AgentDirectoryInput{Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, last.Body.Data.HasMore)
	require.Equal(t, guid.ID(202), last.Body.Data.Items[0].PrincipalID)
	raw, e := json.Marshal(last.Body.Data)
	require.NoError(t, e)
	require.NotContains(t, string(raw), in.SystemPrompt)
	require.NotContains(t, string(raw), "system_prompt")
	require.NotContains(t, string(raw), "credential")
	filtered, e := m.directoryHTTP(ctx, &AgentDirectoryInput{Limit: 50, Search: "Registered"})
	require.NoError(t, e)
	require.Len(t, filtered.Body.Data.Items, 1)
	all, e := m.directoryHTTP(ctx, &AgentDirectoryInput{Limit: 100})
	require.NoError(t, e)
	require.Len(t, all.Body.Data.Items, 2, "foreign tenant must never enter the directory")
	hosted, e := m.directoryHTTP(ctx, &AgentDirectoryInput{Limit: 100, MachineID: in.MachineID.String()})
	require.NoError(t, e)
	require.Len(t, hosted.Body.Data.Items, 1)
	require.Equal(t, in.PrincipalID, hosted.Body.Data.Items[0].PrincipalID)
	_, e = m.directoryHTTP(ctx, &AgentDirectoryInput{Limit: 1, MachineID: in.MachineID.String(), Cursor: first.Body.Data.NextCursor})
	require.Error(t, e, "cursor is bound to the exact machine filter")
	for _, input := range []*AgentDirectoryInput{{Limit: 0}, {Limit: 101}, {Limit: 1, Cursor: "invalid"}, {Limit: 1, Cursor: first.Body.Data.NextCursor, Search: "changed"}, {Limit: 1, Search: strings.Repeat("x", 201)}} {
		_, e := m.directoryHTTP(ctx, input)
		require.Error(t, e)
	}
	other := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 11})
	_, e = m.directoryHTTP(other, &AgentDirectoryInput{Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.Error(t, e)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied, e := m.directoryHTTP(ctx, &AgentDirectoryInput{Limit: 1})
	require.Error(t, e)
	require.Nil(t, denied)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, e = m.directoryHTTP(ctx, &AgentDirectoryInput{Limit: 1})
	require.Error(t, e)
	_, e = (&Module{}).directoryHTTP(ctx, &AgentDirectoryInput{Limit: 1})
	require.Error(t, e)
}

func TestDispatchPrincipalDiscoveryUsesActualTenantRowsAndBoundedSet(t *testing.T) {
	m, db := directoryHarness(t)
	at := time.Now().UTC()
	seed := func(id, tenant, principal guid.ID, state string) {
		t.Helper()
		require.NoError(t, db.Table(m.names.Agents.String()).Create(map[string]any{"id": id, "tenant_id": tenant, "principal_id": principal, "name": "Agent " + id.String(), "handle": "agent-" + id.String(), "avatar_seed": 1, "system_prompt": "Private", "runtime_kind": "CODEX", "model_id": "model", "model_family": "family", "state": state, "created_by_principal_id": 11, "owner_principal_id": 11, "created_at": at, "updated_at": at}).Error)
	}
	seed(2, 7, 300, "ACTIVE")
	seed(3, 7, 200, "STOPPED")
	seed(4, 7, 99, "RETIRED")
	seed(5, 8, 98, "ACTIVE")
	ids, err := m.dispatchPrincipalsOn(t.Context(), db, 7)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{101, 200, 300}, ids)
	ids, err = m.dispatchPrincipalsOn(t.Context(), db, 8)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{98}, ids)
	ids, err = m.dispatchPrincipalsOn(t.Context(), db, 9)
	require.NoError(t, err)
	require.Empty(t, ids)
	for id := guid.ID(10); id < 107; id++ {
		seed(id, 7, id+1000, "ACTIVE")
	}
	ids, err = m.dispatchPrincipalsOn(t.Context(), db, 7)
	require.NoError(t, err)
	require.Len(t, ids, 100)
	seed(107, 7, 1107, "ACTIVE")
	ids, err = m.dispatchPrincipalsOn(t.Context(), db, 7)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Nil(t, ids, "discovery must not silently drop the101st lock participant")
	_, err = m.dispatchPrincipalsOn(t.Context(), nil, 7)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.dispatchPrincipalsOn(t.Context(), db, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	failure := errors.New("principal query failed")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("dispatch_principals_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Agents.String() {
			tx.AddError(failure)
		}
	}))
	_, err = m.dispatchPrincipalsOn(t.Context(), db, 8)
	require.ErrorIs(t, err, failure)
}

func TestDispatchFactsUseObservedMachineRuntimeAndCapacity(t *testing.T) {
	m, db := directoryHarness(t)
	at := time.Now().UTC()
	seedFleetMachine(t, m, db, 2, 7, 102, "ONLINE", &at)
	a := query.BindAgents(db, m.names)
	_, e := a.WithContext(t.Context()).Where(a.ID.Eq(guid.ID(1))).UpdateSimple(a.State.Value("ACTIVE"), a.ProcessStatus.Value("RUNNING"), a.MachineID.Value(guid.ID(2)), a.CapabilityTags.Value(`["go"]`))
	require.NoError(t, e)
	rq := query.BindRuntimes(db, m.names)
	require.NoError(t, rq.WithContext(t.Context()).Create(&model.MachineRuntime{ID: 50, TenantID: 7, MachineID: 2, Kind: "CODEX", Version: "1", ExecutablePath: "/private/bin", ModelsJSON: `["model"]`, InspectedAt: at}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, e := m.dispatchFactsOn(t.Context(), tx, 7)
		require.NoError(t, e)
		require.Len(t, facts, 1)
		require.True(t, facts[0].MachineOnline)
		require.True(t, facts[0].ModelListed)
		require.Equal(t, []string{"go"}, facts[0].Tags)
		data, e := json.Marshal(facts)
		require.NoError(t, e)
		require.NotContains(t, string(data), "/private")
		require.NotContains(t, string(data), "Private prompt")
		foreign, e := m.dispatchFactsOn(t.Context(), tx, 8)
		require.NoError(t, e)
		require.Empty(t, foreign)
		return nil
	}))
	_, e = m.dispatchFactsOn(t.Context(), db, 7)
	require.Error(t, e)
}

func TestDispatchObservedSkillsAreSessionFencedInventory(t *testing.T) {
	m, db := directoryHarness(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	seedFleetMachine(t, m, db, 2, 7, 102, "ONLINE", &at)
	a := query.BindAgents(db, m.names)
	_, err := a.WithContext(t.Context()).Where(a.ID.Eq(guid.ID(1))).UpdateSimple(a.MachineID.Value(guid.ID(2)), a.CapabilityTags.Value(`["go"]`))
	require.NoError(t, err)
	mq := query.BindMachines(db, m.names)
	_, err = mq.WithContext(t.Context()).Where(mq.ID.Eq(guid.ID(2))).UpdateSimple(mq.CurrentSessionID.Value(guid.ID(80)), mq.ConnEpoch.Value(3))
	require.NoError(t, err)
	catalog, err := json.Marshal([]RuntimeDiscovery{{Runtime: "CODEX", State: "ERROR", Source: "CACHED", CheckedAt: &at, LastSuccessAt: &at, Skills: []DiscoveredSkill{{Name: "local-tool", SHA256: strings.Repeat("a", 64)}}}})
	require.NoError(t, err)
	iq := query.BindInspections(db, m.names)
	for _, report := range []model.Inspection{
		{ID: 90, TenantID: 7, MachineID: 2, SessionID: 80, ConnEpoch: 3, Kind: "CAPABILITIES", ScopeKey: "home", State: "READY", ResultJSON: string(catalog), ResultHash: strings.Repeat("b", 64), CreatedAt: at, UpdatedAt: at, ExpiresAt: at},
		{ID: 91, TenantID: 7, MachineID: 2, SessionID: 79, ConnEpoch: 2, Kind: "CAPABILITIES", ScopeKey: "home", State: "READY", ResultJSON: `invalid-old-session`, CreatedAt: at, UpdatedAt: at, ExpiresAt: at},
		{ID: 92, TenantID: 7, MachineID: 2, SessionID: 80, ConnEpoch: 3, Kind: "CAPABILITIES", ScopeKey: "999", State: "READY", ResultJSON: `private-workdir-report`, CreatedAt: at, UpdatedAt: at, ExpiresAt: at},
	} {
		r := report
		require.NoError(t, iq.WithContext(t.Context()).Create(&r))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, err := m.dispatchFactsOn(t.Context(), tx, 7)
		require.NoError(t, err)
		require.Len(t, facts, 1)
		require.NotNil(t, facts[0].ObservedSkills)
		require.EqualValues(t, 90, facts[0].ObservedSkills.InspectionID)
		require.Equal(t, "ERROR", facts[0].ObservedSkills.State, "cached success must not erase the current failed observation")
		require.Equal(t, []string{"go"}, facts[0].Tags, "unapproved local names do not become skill tags")
		return nil
	}))
	_, err = iq.WithContext(t.Context()).Where(iq.ID.Eq(guid.ID(90))).Update(iq.ResultJSON, "invalid-current-session")
	require.NoError(t, err)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.dispatchFactsOn(t.Context(), tx, 7); return err }))
}
