package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestAgentRebindEveryStorageFailureRollsBackCredentialAndBinding(t *testing.T) {
	for _, operation := range []string{"query", "create", "update"} {
		exercise := func(t *testing.T, failAt int) int {
			f := agentRebindHarness(t)
			before := f.agent(t)
			count := 0
			injected := errors.New("binding storage unavailable")
			hook := func(tx *gorm.DB) {
				if !strings.HasPrefix(tx.Statement.Table, "dir_fleet_") {
					return
				}
				count++
				if count == failAt {
					tx.AddError(injected)
				}
			}
			switch operation {
			case "query":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("binding_storage_failure", hook))
			case "create":
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("binding_storage_failure", hook))
			case "update":
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("binding_storage_failure", hook))
			}
			_, err := f.rebind()
			require.NoError(t, f.db.Callback().Query().Remove("binding_storage_failure"))
			require.NoError(t, f.db.Callback().Create().Remove("binding_storage_failure"))
			require.NoError(t, f.db.Callback().Update().Remove("binding_storage_failure"))
			if failAt == 0 {
				require.NoError(t, err)
				return count
			}
			require.ErrorIs(t, err, injected)
			require.Equal(t, before, f.agent(t))
			q := query.BindAgentMachineCommands(f.db, f.m.names)
			n, e := q.WithContext(t.Context()).Count()
			require.NoError(t, e)
			require.Zero(t, n)
			var effects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Where("kind=?", "credential_rotated").Count(&effects).Error)
			require.Zero(t, effects)
			return count
		}
		count := exercise(t, 0)
		require.Positive(t, count, "fault injection must observe actual storage")
		for point := 1; point <= count; point++ {
			t.Run(fmt.Sprintf("%s/%d", operation, point), func(t *testing.T) { exercise(t, point) })
		}
	}
}

func TestAgentRebindCurrentDependenciesAndAuthorityFailuresNeverRotate(t *testing.T) {
	for _, fault := range []string{"principal-lock", "missing-lock", "account-query", "account-inactive", "missing-account", "quiet-query", "missing-quiet", "missing-rotation", "rotation", "encryption", "runtime-path", "revoked-target"} {
		t.Run(fault, func(t *testing.T) {
			f := agentRebindHarness(t)
			injected := errors.New("current binding authority unavailable")
			switch fault {
			case "principal-lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "missing-lock":
				f.m.principals.Lock = nil
			case "account-query":
				f.m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, injected }
			case "account-inactive":
				f.m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			case "missing-account":
				f.m.accounts = nil
			case "quiet-query":
				f.m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected }
			case "missing-quiet":
				f.m.agentRebindQuiet = nil
			case "missing-rotation":
				f.m.agentCredentialRotate = nil
			case "rotation":
				f.m.agentCredentialRotate = func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID) (identity.ServiceAccountCredentialSecret, error) {
					return identity.ServiceAccountCredentialSecret{}, injected
				}
			case "encryption":
				f.m.config.GatewayEncryptionKey = "invalid"
			case "runtime-path":
				require.NoError(t, f.db.Table(f.m.names.Runtimes.String()).Where("machine_id=?", 2).Update("executable_path", "").Error)
			case "revoked-target":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 2).Updates(map[string]any{"state": "REVOKED", "name_key": nil}).Error)
			}
			before := f.agent(t)
			_, err := f.rebind()
			require.Error(t, err)
			require.Equal(t, before, f.agent(t))
			var effects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Where("kind=?", "credential_rotated").Count(&effects).Error)
			require.Zero(t, effects)
		})
	}
}

type rebindFixture struct {
	m                           *Module
	db                          *gorm.DB
	human, oldRunner, newRunner context.Context
	oldSession, newSession      SessionFact
	publisher                   *fleetPublisher
}

func TestAgentRetirementBindingExposesDurableProtocolForWizardRecovery(t *testing.T) {
	f := agentRebindHarness(t)
	router := fleetBusinessRouter(t, f.m)
	q := query.BindAgents(f.db, f.m.names)
	id := guid.ID(9007199254740997)
	for _, state := range []string{"RETIRING", "RETIRED", "STOPPED"} {
		_, err := q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(7)), q.PrincipalID.Eq(guid.ID(202))).Updates(map[string]any{"retirement_id": id, "state": state, "version": 15})
		require.NoError(t, err)
		response := fleetHTTPRequest(router, "GET", "/tenants/7/agents/202/machine-binding", "", nil)
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"retirement_id":"9007199254740997"`)
		require.Contains(t, response.Body.String(), `"version":15`)
	}
	raw, err := json.Marshal(bindingView(&model.Agent{PrincipalID: 202}))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "retirement_id", "an Agent without a retirement must show the start form")
}

func agentRebindHarness(t *testing.T) *rebindFixture {
	t.Helper()
	m, db, human, runner, oldSession, p, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(human, tx, 7, in); return e }))
	q := query.BindAgents(db, m.names)
	_, err := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).Updates(map[string]any{"state": "STOPPED"})
	require.NoError(t, err)
	now := time.Now().UTC()
	key := "second-machine"
	require.NoError(t, query.BindMachines(db, m.names).WithContext(t.Context()).Create(&model.Machine{ID: 2, TenantID: 7, PrincipalID: 102, Name: "Second machine", NameKey: &key, State: "OFFLINE", OS: "WINDOWS", SlotCount: 1, Version: 1, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, query.BindRuntimes(db, m.names).WithContext(t.Context()).Create(&model.MachineRuntime{ID: 81, TenantID: 7, MachineID: 2, Kind: "CLAUDE_CODE", Version: "1", ExecutablePath: "/bin/claude", ModelsJSON: "[]", InspectedAt: now}))
	m.accounts = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, _ time.Time) (bool, error) {
		return tenant == 7 && (principal == 101 || principal == 102 || principal == 202), nil
	}
	nextRunner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 102, SubjectType: authn.SubjectTypePrincipal})
	var nextSession SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		nextSession, e = m.OpenSessionOn(nextRunner, tx, 7, 2, "new-agent-runner")
		return e
	}))
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
	m.agentCredentialRotate = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ *guid.ID) (identity.ServiceAccountCredentialSecret, error) {
		e := tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES (?)", "credential_rotated").Error
		return identity.ServiceAccountCredentialSecret{Credential: identity.ServiceAccountCredential{ID: 303, ServiceAccountID: 202}, Secret: "private-agent-secret"}, e
	}
	return &rebindFixture{m: m, db: db, human: human, oldRunner: runner, newRunner: nextRunner, oldSession: oldSession, newSession: nextSession, publisher: p}
}
func (f *rebindFixture) rebind() (AgentMachineBinding, error) {
	var out AgentMachineBinding
	e := f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = f.m.RebindAgentOn(f.human, tx, 7, 202, 1, AgentMachineBindingInput{MachineID: 2, RuntimeKind: "CLAUDE_CODE", ModelID: "sonnet"})
		return e
	})
	return out, e
}
func (f *rebindFixture) agent(t *testing.T) *model.Agent {
	t.Helper()
	q := query.BindAgents(f.db, f.m.names)
	r, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).First()
	require.NoError(t, e)
	return r
}
func TestRebindRotatesAndQueuesOnlyEncryptedCredentialUntilRunnerAcknowledges(t *testing.T) {
	f := agentRebindHarness(t)
	out, err := f.rebind()
	require.NoError(t, err)
	require.EqualValues(t, 2, out.MachineID)
	require.Equal(t, "PENDING", out.CredentialDelivery)
	require.EqualValues(t, 1, *out.PendingCleanupMachineID)
	require.Equal(t, 2, out.ConfigRevision)
	q := query.BindAgentMachineCommands(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NotEmpty(t, rows[0].SealedCredential)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-agent-secret")
	require.Equal(t, "PENDING", f.agent(t).CredentialDelivery)
}
func TestRebindFailureRollsBackCredentialBindingAndCommands(t *testing.T) {
	for _, fault := range []string{"running", "execution", "same-machine", "outbox", "runtime"} {
		t.Run(fault, func(t *testing.T) {
			f := agentRebindHarness(t)
			switch fault {
			case "running":
				q := query.BindAgents(f.db, f.m.names)
				_, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).Update(q.ProcessStatus, "RUNNING")
				require.NoError(t, e)
			case "execution":
				f.m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil }
			case "same-machine":
				q := query.BindAgents(f.db, f.m.names)
				_, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).Update(q.MachineID, guid.ID(2))
				require.NoError(t, e)
			case "outbox":
				f.publisher.fail = true
			case "runtime":
				q := query.BindRuntimes(f.db, f.m.names)
				_, e := q.WithContext(t.Context()).Where(q.MachineID.Eq(guid.ID(2))).Delete()
				require.NoError(t, e)
			}
			before := f.agent(t)
			_, err := f.rebind()
			require.Error(t, err)
			require.Equal(t, before, f.agent(t))
			q := query.BindAgentMachineCommands(f.db, f.m.names)
			n, err := q.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
			var effects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Where("kind=?", "credential_rotated").Count(&effects).Error)
			require.Zero(t, effects)
		})
	}
}

func TestRepeatedRebindCannotOverwriteOutstandingOldCleanup(t *testing.T) {
	f := agentRebindHarness(t)
	_, err := f.rebind()
	require.NoError(t, err)
	before := f.agent(t)
	err = f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.RebindAgentOn(f.human, tx, 7, 202, before.Version, AgentMachineBindingInput{MachineID: 1, RuntimeKind: "CLAUDE_CODE", ModelID: "sonnet"})
		return e
	})
	require.ErrorIs(t, err, ErrVersionConflict)
	require.Equal(t, before, f.agent(t))
}

func TestRebindMCPRequiresExactObservedNamesAndReportsUnknown(t *testing.T) {
	for _, mode := range []string{"known", "unknown", "missing", "stale", "cached-error"} {
		t.Run(mode, func(t *testing.T) {
			f := agentRebindHarness(t)
			q := query.BindAgents(f.db, f.m.names)
			_, err := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).Update(q.MCPAllowlist, `["reader","writer"]`)
			require.NoError(t, err)
			if mode != "missing" {
				at := time.Now().UTC()
				d := RuntimeDiscovery{Runtime: "CLAUDE_CODE", State: "READY", Source: "LOCAL_CONFIG", MCP: []string{"reader", "writer"}, LastSuccessAt: &at}
				if mode == "unknown" {
					d.MCP = []string{"reader", "Writer"}
				}
				if mode == "stale" {
					at = at.Add(-25 * time.Hour)
				}
				if mode == "cached-error" {
					d.State, d.Source, d.Reason = "ERROR", "CACHED", "LOCAL_CONFIG_UNREADABLE"
				}
				b, e := json.Marshal([]RuntimeDiscovery{d})
				require.NoError(t, e)
				require.NoError(t, query.BindInspections(f.db, f.m.names).WithContext(t.Context()).Create(&model.Inspection{ID: 90001, TenantID: 7, MachineID: 2, ConnEpoch: 1, SessionID: f.newSession.ID, Kind: "CAPABILITIES", ScopeKey: "home", State: "READY", ResultJSON: string(b), CreatedAt: at, UpdatedAt: at, ExpiresAt: at}))
			}
			out, err := f.rebind()
			if mode == "unknown" || mode == "missing" {
				require.ErrorIs(t, err, ErrAgentRebindMCP)
				var detail *RebindCapabilityError
				require.ErrorAs(t, err, &detail)
				if mode == "unknown" {
					require.Equal(t, []string{"writer"}, detail.Unknown)
				} else {
					require.Equal(t, []string{"reader", "writer"}, detail.Unknown)
				}
				require.EqualValues(t, 1, *f.agent(t).MachineID)
				var effects int64
				require.NoError(t, f.db.Table("test_fleet_effects").Where("kind=?", "credential_rotated").Count(&effects).Error)
				require.Zero(t, effects)
			} else {
				require.NoError(t, err)
				require.Equal(t, mode != "known", out.CapabilityStale)
			}
		})
	}
}

func TestRebindReasoningMustMatchExactModelEvidence(t *testing.T) {
	for _, mode := range []string{"supported", "other-model", "no-evidence", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			f := agentRebindHarness(t)
			effort := "high"
			q := query.BindAgents(f.db, f.m.names)
			_, err := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).Update(q.ReasoningEffort, effort)
			require.NoError(t, err)
			if mode != "no-evidence" {
				at := time.Now().UTC()
				modelID := "sonnet"
				if mode == "other-model" {
					modelID = "other"
				}
				efforts := []string{"low", "high"}
				if mode == "unsupported" {
					efforts = []string{"low"}
				}
				b, e := json.Marshal([]RuntimeDiscovery{{Runtime: "CLAUDE_CODE", State: "READY", Source: "CLI_CACHE", LastSuccessAt: &at, Reasoning: []ModelReasoning{{Model: modelID, Efforts: efforts}}}})
				require.NoError(t, e)
				require.NoError(t, query.BindInspections(f.db, f.m.names).WithContext(t.Context()).Create(&model.Inspection{ID: 90002, TenantID: 7, MachineID: 2, ConnEpoch: 1, SessionID: f.newSession.ID, Kind: "MODELS", ScopeKey: "home", State: "READY", ResultJSON: string(b), CreatedAt: at, UpdatedAt: at, ExpiresAt: at}))
			}
			out, err := f.rebind()
			if mode == "supported" {
				require.NoError(t, err)
				require.Equal(t, &effort, out.ReasoningEffort)
				require.Equal(t, &effort, f.agent(t).ReasoningEffort)
			} else {
				require.ErrorIs(t, err, ErrAgentRebindEffort)
				require.EqualValues(t, 1, *f.agent(t).MachineID)
			}
		})
	}
}
