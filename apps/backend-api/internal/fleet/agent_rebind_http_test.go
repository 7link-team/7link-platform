package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRebindHTTPReplayRechecksCurrentAuthorityAndNeverReturnsCredential(t *testing.T) {
	f := agentRebindHarness(t)
	store := idempotency.NewModule(f.m.runtime)
	raw, err := f.db.DB()
	require.NoError(t, err)
	source := store.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, f.m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	f.m.idempotency = store
	router := fleetBusinessRouter(t, f.m)
	headers := map[string]string{"Idempotency-Key": "rebind-exact-command", "If-Match": "\"1\""}
	body := `{"machine_id":"2","runtime_kind":"CLAUDE_CODE","model_id":"sonnet"}`
	first := fleetHTTPRequest(router, "PUT", "/tenants/7/agents/202/machine-binding", body, headers)
	require.Equal(t, 200, first.Code, first.Body.String())
	require.NotContains(t, first.Body.String(), "private-agent-secret")
	require.NotContains(t, first.Body.String(), "client_secret")
	replay := fleetHTTPRequest(router, "PUT", "/tenants/7/agents/202/machine-binding", body, headers)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	var a, b struct {
		Data AgentMachineBinding `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &b))
	require.Equal(t, a.Data, b.Data)
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied := fleetHTTPRequest(router, "PUT", "/tenants/7/agents/202/machine-binding", body, headers)
	require.Equal(t, 403, denied.Code, denied.Body.String())
}

func TestRebindHTTPUnknownMCPHasStructuredBusinessDetails(t *testing.T) {
	err := fleetHTTPError(t.Context(), &RebindCapabilityError{Cause: ErrAgentRebindMCP, Unknown: []string{"reader"}, Stale: true})
	require.True(t, respx.IsStatusError(err))
	b, e := json.Marshal(err)
	require.NoError(t, e)
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Unknown []string `json:"unknown"`
			Stale   bool     `json:"stale"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(b, &envelope))
	require.Equal(t, 142007, envelope.Code)
	require.Equal(t, []string{"reader"}, envelope.Data.Unknown)
	require.True(t, envelope.Data.Stale)
}

func TestAgentBindingHTTPReadsRealCommandsAndRunnerAcknowledgements(t *testing.T) {
	f := agentRebindHarness(t)
	_, e := f.rebind()
	require.NoError(t, e)
	path := &RebindAgentPathInput{AgentPrincipalID: "202"}
	binding, e := f.m.getAgentBindingHTTP(f.human, path)
	require.NoError(t, e)
	require.EqualValues(t, 2, binding.Body.Data.MachineID)
	commands, e := f.m.listAgentMachineCommandsHTTP(f.human, path)
	require.NoError(t, e)
	require.Len(t, commands.Body.Data, 2)
	serialized, e := json.Marshal(commands)
	require.NoError(t, e)
	require.NotContains(t, string(serialized), "private-agent-secret")
	require.NotContains(t, string(serialized), "sealed_credential")
	for _, machine := range []string{"1", "2"} {
		ctx, session := f.oldRunner, f.oldSession
		if machine == "2" {
			ctx, session = f.newRunner, f.newSession
		}
		pull := &heartbeatInput{MachineInput: MachineInput{MachineID: machine}}
		pull.Body.SessionID = session.ID
		pull.Body.ConnEpoch = session.ConnEpoch
		items, e := f.m.pullAgentMachineCommandsHTTP(ctx, pull)
		require.NoError(t, e)
		require.Equal(t, "no-store", items.CacheControl)
		require.Len(t, items.Body.Data, 1)
		command := items.Body.Data[0]
		report := &agentMachineCommandReportInput{MachineInput: MachineInput{MachineID: machine}, CommandID: command.ID.String()}
		report.Body.SessionID = session.ID
		report.Body.ConnEpoch = session.ConnEpoch
		report.Body.State = "DONE"
		completed, e := f.m.reportAgentMachineCommandHTTP(ctx, report)
		require.NoError(t, e)
		require.Equal(t, "DONE", completed.Body.Data.State)
		replay, e := f.m.reportAgentMachineCommandHTTP(ctx, report)
		require.NoError(t, e)
		require.Equal(t, completed.Body.Data, replay.Body.Data)
	}
	require.Nil(t, f.agent(t).PendingCleanupMachineID)
	require.Equal(t, "DELIVERED", f.agent(t).CredentialDelivery)
	for _, selector := range []string{"bad", "999"} {
		_, e = f.m.getAgentBindingHTTP(f.human, &RebindAgentPathInput{AgentPrincipalID: selector})
		require.Error(t, e)
	}
	_, e = f.m.listAgentMachineCommandsHTTP(f.human, &RebindAgentPathInput{AgentPrincipalID: "bad"})
	require.Error(t, e)
	_, e = f.m.pullAgentMachineCommandsHTTP(f.oldRunner, &heartbeatInput{MachineInput: MachineInput{MachineID: "bad"}})
	require.Error(t, e)
	_, e = f.m.reportAgentMachineCommandHTTP(f.oldRunner, &agentMachineCommandReportInput{MachineInput: MachineInput{MachineID: "1"}, CommandID: "bad"})
	require.Error(t, e)
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = f.m.getAgentBindingHTTP(f.human, path)
	require.Error(t, e)
	_, e = f.m.listAgentMachineCommandsHTTP(f.human, path)
	require.Error(t, e)
}

func TestAgentBindingHTTPStorageFailuresReturnNoPartialReceipt(t *testing.T) {
	for _, operation := range []string{"binding", "commands", "pull", "report"} {
		t.Run(operation, func(t *testing.T) {
			f := agentRebindHarness(t)
			_, e := f.rebind()
			require.NoError(t, e)
			injected := errors.New("binding read unavailable")
			table := f.m.names.AgentMachineCommands.String()
			if operation == "binding" {
				table = f.m.names.Agents.String()
			}
			require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("binding_http_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(injected)
				}
			}))
			switch operation {
			case "binding":
				response, e := f.m.getAgentBindingHTTP(f.human, &RebindAgentPathInput{AgentPrincipalID: "202"})
				require.Error(t, e)
				require.Nil(t, response)
			case "commands":
				response, e := f.m.listAgentMachineCommandsHTTP(f.human, &RebindAgentPathInput{AgentPrincipalID: "202"})
				require.Error(t, e)
				require.Nil(t, response)
			case "pull":
				in := &heartbeatInput{MachineInput: MachineInput{MachineID: "1"}}
				in.Body.SessionID = f.oldSession.ID
				in.Body.ConnEpoch = f.oldSession.ConnEpoch
				response, e := f.m.pullAgentMachineCommandsHTTP(f.oldRunner, in)
				require.Error(t, e)
				require.Nil(t, response)
			case "report":
				in := &agentMachineCommandReportInput{MachineInput: MachineInput{MachineID: "1"}, CommandID: "1"}
				in.Body.SessionID = f.oldSession.ID
				in.Body.ConnEpoch = f.oldSession.ConnEpoch
				in.Body.State = "DONE"
				response, e := f.m.reportAgentMachineCommandHTTP(f.oldRunner, in)
				require.Error(t, e)
				require.Nil(t, response)
			}
		})
	}
}
