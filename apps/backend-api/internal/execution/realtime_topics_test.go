package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkspaceWritePlanAndMissingApprovalFailClosed(t *testing.T) {
	for _, mode := range []string{"READ_ONLY", "WORKSPACE_WRITE", "ON_REQUEST", "FULL_AUTO"} {
		t.Run(mode, func(t *testing.T) {
			f := executionHarness(t)
			in := validCreate()
			in.Plan.AuthorizationMode = mode
			var v ExecutionView
			err := f.db.Transaction(func(tx *gorm.DB) error {
				var err error
				v, err = f.m.CreateOn(f.human, tx, 7, in)
				return err
			})
			if mode == "FULL_AUTO" {
				require.NoError(t, err)
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					commands, e := f.m.PullOn(f.runner, tx, 7, f.fence)
					require.Empty(t, commands, "an unapproved plan must never lease")
					return e
				}))
				require.Nil(t, f.read(t, v.ID).LeaseExpiresAt)
				return
			}
			require.NoError(t, err)
			require.Equal(t, mode, v.Plan.AuthorizationMode)
			require.Equal(t, "/registered/workspace", v.Plan.Workdir)
			f.pull(t, v.ID)
		})
	}
}

func TestAgentDispatchRejectsChangedActualBindingBeforeLease(t *testing.T) {
	for _, fault := range []string{"machine", "unbound", "runtime", "offline", "missing", "error", "inactive", "model", "role"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
			agent := fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", Roles: []string{"EXECUTOR"}, RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "family"}
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return agent, true, nil
			}
			v := f.create(t)
			machine := guid.ID(1)
			fact := fleet.AgentRuntimeFact{MachineID: &machine, MachineState: "ONLINE", RuntimeKind: "CODEX"}
			var providerErr error
			f.m.agentRuntime.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
				return fact, true, providerErr
			}
			switch fault {
			case "machine":
				machine = 2
			case "unbound":
				fact.MachineID = nil
			case "runtime":
				fact.RuntimeKind = "CLAUDE_CODE"
			case "offline":
				fact.MachineState = "OFFLINE"
			case "missing":
				f.m.agentRuntime.ByPrincipalOn = nil
			case "error":
				providerErr = errors.New("fleet unavailable")
			case "inactive":
				agent.State = "DISABLED"
			case "model":
				agent.ModelID = "changed-model"
			case "role":
				agent.Roles = []string{"REVIEWER"}
			}
			err := f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.PullOn(f.runner, tx, 7, f.fence); return e })
			require.Error(t, err)
			q := query.BindExecutions(f.db, f.m.names)
			row, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).First()
			require.NoError(t, err)
			require.Nil(t, row.LeaseExpiresAt)
			require.Equal(t, "CREATED", row.State)
		})
	}
}

func TestExecutionRealtimeUsesCurrentReadAuthority(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideRealtimeTopics(s))
	require.NoError(t, s.Seal())
	p, err := module.Resolve[RealtimeTopics](s)
	require.NoError(t, err)
	allowed, err := p.Authorize(f.human, 7, 11, v.ID, false)
	require.NoError(t, err)
	require.True(t, allowed)
	for _, call := range []struct {
		ctx                   context.Context
		tenant, principal, id guid.ID
		collection            bool
	}{
		{f.human, 7, 11, v.ID, true}, {f.human, 8, 11, v.ID, false}, {f.human, 7, 12, v.ID, false}, {f.human, 7, 11, v.ID + 1, false},
		{authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 12}), 7, 11, v.ID, false},
	} {
		allowed, err = p.Authorize(call.ctx, call.tenant, call.principal, call.id, call.collection)
		require.NoError(t, err)
		require.False(t, allowed)
	}
	f.authorized = false
	allowed, err = p.Authorize(f.human, 7, 11, v.ID, false)
	require.NoError(t, err)
	require.False(t, allowed)
	f.authorized = true
	f.humanActive = false
	allowed, err = p.Authorize(f.human, 7, 11, v.ID, false)
	require.NoError(t, err)
	require.False(t, allowed)
	f.m.runtime = nil
	allowed, err = p.Authorize(f.human, 7, 11, v.ID, false)
	require.ErrorIs(t, err, ErrUnavailable)
	require.False(t, allowed)
}

func TestExecutionHTTPPreparesFullAutoWithoutGrantingTools(t *testing.T) {
	_, handler := executionRoutesFixture(t)
	for _, mode := range []string{"FULL_AUTO"} {
		in := validCreate()
		in.Plan.AuthorizationMode = mode
		response := executionRouteRequest(t, handler, http.MethodPost, "/tenants/7/executions", in, 0, false)
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "FULL_AUTO_APPROVAL_REQUIRED")
	}
}

func TestLegacyUnsupportedAuthorizationCannotAcquireNewLease(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	for _, mode := range []string{"ON_REQUEST", "FULL_AUTO"} {
		plan := v.Plan
		plan.AuthorizationMode = mode
		encoded, err := json.Marshal(plan)
		require.NoError(t, err)
		q := query.BindExecutions(f.db, f.m.names)
		_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Updates(map[string]any{"plan_json": string(encoded), "plan_hash": hash(encoded)})
		require.NoError(t, err)
		err = f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.PullOn(f.runner, tx, 7, f.fence); return err })
		require.ErrorIs(t, err, ErrApprovalUnavailable)
		require.Nil(t, f.read(t, v.ID).LeaseExpiresAt)
	}
}

func TestAgentExecutionPermissionCapAndServerFrozenPersona(t *testing.T) {
	for _, cap := range []string{"READ_ONLY", "WORKSPACE_WRITE", "ON_REQUEST", "FULL_AUTO"} {
		t.Run(cap, func(t *testing.T) {
			f := executionHarness(t)
			f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
			agent := fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", Roles: []string{"EXECUTOR"}, RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "family", AuthorizationMode: cap, SystemPrompt: "Actual configured persona"}
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return agent, true, nil
			}
			in := validCreate()
			in.Plan.AuthorizationMode = "WORKSPACE_WRITE"
			in.Plan.AgentSystemPrompt = "Posted impostor persona"
			var v ExecutionView
			err := f.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = f.m.CreateOn(f.human, tx, 7, in); return e })
			if cap != "WORKSPACE_WRITE" {
				require.ErrorIs(t, err, ErrForbidden)
				return
			}
			require.NoError(t, err)
			require.Equal(t, agent.SystemPrompt, v.Plan.AgentSystemPrompt)
			agent.AuthorizationMode = "READ_ONLY"
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.PullOn(f.runner, tx, 7, f.fence); return e }), ErrForbidden)
			agent.AuthorizationMode = "WORKSPACE_WRITE"
			agent.SystemPrompt = "Edited after approval"
			leased := f.pull(t, v.ID)
			require.Equal(t, "Actual configured persona", leased.Plan.AgentSystemPrompt)
		})
	}
	f := executionHarness(t)
	in := validCreate()
	in.Plan.AgentSystemPrompt = "Untrusted human payload"
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		v, e := f.m.CreateOn(f.human, tx, 7, in)
		require.Empty(t, v.Plan.AgentSystemPrompt)
		return e
	}))
}

func TestProvideRealtimeTopicsRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideRealtimeTopics(s))
	require.ErrorIs(t, f.m.provideRealtimeTopics(s), module.ErrServiceDuplicate)
}
