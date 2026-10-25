package fleet

import (
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func agentRegistrationHarness(t *testing.T) (*Module, *gorm.DB, context.Context, context.Context, SessionFact, *fleetPublisher, AgentRegistration) {
	m, db, runner, p := machineHarness(t)
	m.accounts = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, _ time.Time) (bool, error) {
		return tenant == 7 && (principal == 101 || principal == 202), nil
	}
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 11}, true, nil
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.agentRoles = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, keys []string) error {
		require.Equal(t, guid.ID(202), principal)
		require.Equal(t, []string{"agent_executor"}, keys)
		return nil
	}
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		session, err = m.OpenSessionOn(runner, tx, 7, 1, "agent-runner")
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.RecordInventoryOn(runner, tx, 7, 1, session.ID, session.ConnEpoch, Inventory{Runtimes: []RuntimeObservation{{Kind: "CLAUDE_CODE", Version: "1", ExecutablePath: "/bin/claude", Models: []string{}}}})
	}))
	human := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11})
	in := AgentRegistration{PrincipalID: 202, MachineID: 1, Name: "Registered executor", Handle: "registered-executor", Runtime: "CLAUDE_CODE", Model: "sonnet", Roles: []string{"EXECUTOR"}, SystemPrompt: "Implement only the assigned plan."}
	return m, db, human, runner, session, p, in
}
func TestAgentRegistrationRequiresActualAccountRolesAndObservedRuntime(t *testing.T) {
	m, db, ctx, _, _, _, in := agentRegistrationHarness(t)
	var out RegisteredAgent
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; out, err = m.RegisterAgentOn(ctx, tx, 7, in); return err }))
	require.Positive(t, out.ID)
	require.Equal(t, "ACTIVE", out.State)
	require.Equal(t, "NONE", out.ProcessStatus)
	require.Equal(t, "anthropic", out.ModelFamily)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		a, found, err := m.ByPrincipalOn(ctx, tx, 7, 202)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, []string{"EXECUTOR"}, a.Roles)
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.RegisterAgentOn(ctx, tx, 7, in); return err }), ErrVersionConflict)
}
func TestAgentRegistrationFailuresRollbackIdentityAndMappedRoles(t *testing.T) {
	for _, fault := range []string{"foreign_account", "role_grant", "missing_roles", "human_authority", "machine_principal", "offline", "runtime_missing", "family", "invalid_roles", "handle", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _, p, in := agentRegistrationHarness(t)
			switch fault {
			case "foreign_account":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			case "role_grant":
				m.agentRoles = func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error { return ErrMachineForbidden }
			case "missing_roles":
				m.agentRoles = nil
			case "human_authority":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "machine_principal":
				in.PrincipalID = 101
				m.agentRoles = func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error { return nil }
			case "offline":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", 1).Update("state", "OFFLINE").Error)
			case "runtime_missing":
				in.Runtime = "CODEX"
				in.Model = "gpt-test"
			case "family":
				in.Model = "bad model"
			case "invalid_roles":
				in.Roles = []string{"EXECUTOR", "ACCEPTOR"}
			case "handle":
				in.Handle = "../outside"
			case "outbox":
				p.fail = true
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.RegisterAgentOn(ctx, tx, 7, in); return err })
			require.Error(t, err)
			q := query.BindAgents(db, m.names)
			n, err := q.WithContext(ctx).Where(q.PrincipalID.Eq(guid.ID(202))).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestAgentRegistrationHTTPReplayRechecksCurrentHumanAuthority(t *testing.T) {
	m, db, ctx, _, _, _, in := agentRegistrationHarness(t)
	in.AuthorizationMode = "WORKSPACE_WRITE"
	store := idempotency.NewModule(m.runtime)
	raw, err := db.DB()
	require.NoError(t, err)
	source := store.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	m.idempotency = store
	router := fleetBusinessRouter(t, m)
	body, err := json.Marshal(in)
	require.NoError(t, err)
	headers := map[string]string{"Idempotency-Key": "real-agent-registration"}
	first := fleetHTTPRequest(router, "POST", "/tenants/7/agents", string(body), headers)
	require.Equal(t, 201, first.Code, first.Body.String())
	replay := fleetHTTPRequest(router, "POST", "/tenants/7/agents", string(body), headers)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	var a, b struct {
		Data RegisteredAgent `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &b))
	require.Equal(t, a.Data, b.Data)
	in.Name = "different request"
	body, err = json.Marshal(in)
	require.NoError(t, err)
	conflict := fleetHTTPRequest(router, "POST", "/tenants/7/agents", string(body), headers)
	require.Equal(t, 409, conflict.Code, conflict.Body.String())
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		a, ok, e := m.ByPrincipalOn(ctx, tx, 7, 202)
		require.NoError(t, e)
		require.True(t, ok)
		require.Equal(t, "WORKSPACE_WRITE", a.AuthorizationMode)
		require.Equal(t, in.SystemPrompt, a.SystemPrompt)
		return nil
	}))
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied := fleetHTTPRequest(router, "POST", "/tenants/7/agents", string(body), headers)
	require.Equal(t, 403, denied.Code, denied.Body.String())
}
func TestAgentRegistrationRejectsUnsupportedPermissionCaps(t *testing.T) {
	for _, mode := range []string{"ON_REQUEST", "FULL_AUTO", "BYPASS"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, _, _, _, in := agentRegistrationHarness(t)
			in.AuthorizationMode = mode
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(ctx, tx, 7, in); return e }), ErrInvalidArgument)
		})
	}
}

func TestAgentRegistrationHTTPUnavailableAndOfflineAreDistinct(t *testing.T) {
	for _, fault := range []string{"offline", "missing_idempotency", "invalid_role", "account_error", "missing_writer"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _, _, in := agentRegistrationHarness(t)
			status := 503
			switch fault {
			case "offline":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", 1).Update("state", "OFFLINE").Error)
				status = 409
			case "invalid_role":
				in.Roles = []string{"UNKNOWN"}
				status = 422
			case "account_error":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) {
					return false, ErrUnavailable
				}
			case "missing_writer":
				m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("dir_"))
			}
			if fault != "missing_idempotency" && fault != "missing_writer" {
				store := idempotency.NewModule(m.runtime)
				raw, e := db.DB()
				require.NoError(t, e)
				source := store.MigrationSource()
				source.Prefix = "dir_"
				require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
				services := module.NewServices()
				require.NoError(t, module.Provide[guid.Source](services, m.ids))
				require.NoError(t, services.Seal())
				require.NoError(t, store.InjectServices(services))
				m.idempotency = store
			}
			router := fleetBusinessRouter(t, m)
			body, e := json.Marshal(in)
			require.NoError(t, e)
			response := fleetHTTPRequest(router, "POST", "/tenants/7/agents", string(body), map[string]string{"Idempotency-Key": "offline-agent"})
			require.Equal(t, status, response.Code, response.Body.String())
			var count int64
			require.NoError(t, db.Table(m.names.Agents.String()).Where("principal_id=?", 202).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestAgentRegistrationRoleConnectorRejectsMissingAndRepeatedBinding(t *testing.T) {
	m := &Module{}
	services := module.NewServices()
	require.NoError(t, m.provideAgentRegistration(services))
	require.NoError(t, services.Seal())
	connector, e := module.Resolve[AgentRegistrationRolesConnector](services)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(nil), ErrUnavailable)
	validate := func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error { return nil }
	require.NoError(t, connector.Bind(validate))
	require.ErrorIs(t, connector.Bind(validate), ErrUnavailable)
}
