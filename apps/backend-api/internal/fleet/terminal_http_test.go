package fleet

import (
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestTerminalPolicyCurrentPrincipalFailuresPreservePersistedAccess(t *testing.T) {
	for _, fault := range []string{"lock", "human-source", "permission-source", "missing-permission", "inactive-human", "denied"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := terminalFixture(t)
			_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, err)
			injected := errors.New("remote access current authority unavailable")
			switch fault {
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "human-source":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, injected
				}
			case "permission-source":
				m.terminal.permission = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, injected
				}
			case "missing-permission":
				m.terminal.permission = nil
			case "inactive-human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "denied":
				m.terminal.permission = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, nil
				}
			}
			_, err = m.SetTerminalAccess(ctx, 7, 1, 2, false, "", "")
			require.Error(t, err)
			var state struct {
				Enabled bool
				Version int
			}
			require.NoError(t, db.Table(m.names.TerminalAccess.String()).Where("machine_id=?", 1).Take(&state).Error)
			require.True(t, state.Enabled)
			require.Equal(t, 2, state.Version)
		})
	}
}

func TestTerminalHTTPStorageOutageReturnsUnavailableWithoutPartialResources(t *testing.T) {
	m, _, ctx, row, _ := terminalFixture(t)
	m.revokeBrowser.ValidateCSRF = func(string, string, string) error { return nil }
	m.runtime = nil
	for _, call := range []func() error{
		func() error {
			out, e := m.terminalAccessHTTP(ctx, &MachineInput{MachineID: "1"})
			require.Nil(t, out)
			return e
		},
		func() error {
			out, e := m.setTerminalAccessHTTP(ctx, &terminalAccessInput{MachineInput: MachineInput{MachineID: "1"}, IfMatch: "1"})
			require.Nil(t, out)
			return e
		},
		func() error {
			out, e := m.getTerminalHTTP(ctx, &TerminalIDInput{ID: row.ID.String()})
			require.Nil(t, out)
			return e
		},
		func() error {
			out, e := m.revokeTerminalHTTP(ctx, &terminalRevokeInput{TerminalIDInput: TerminalIDInput{ID: row.ID.String()}})
			require.Nil(t, out)
			return e
		},
	} {
		var status huma.StatusError
		require.ErrorAs(t, call(), &status)
		require.Equal(t, 503, status.GetStatus())
	}
	_, found, e := m.terminalOwner(ctx, row.ID)
	require.ErrorIs(t, e, ErrUnavailable)
	require.False(t, found)
	_, e = m.openTerminalReceipt(ctx, 1, &terminalOpenInput{})
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestTerminalHTTPPolicyMetadataAndRevokeUseCurrentStoredSession(t *testing.T) {
	m, db, ctx, row, _ := terminalFixture(t)
	bindTerminalRecordingFixture(t, m, db, ctx)
	m.revokeBrowser.ValidateCSRF = func(method, origin, cookie string) error {
		if cookie != "browser" || origin != "https://console.invalid" {
			return errors.New("csrf rejected")
		}
		return nil
	}
	in := &terminalAccessInput{MachineInput: MachineInput{MachineID: "1"}, IfMatch: `"1"`, Cookie: "browser", Origin: "https://console.invalid"}
	in.Body.Enabled, in.Body.Proof = true, "fresh"
	policy, err := m.setTerminalAccessHTTP(ctx, in)
	require.NoError(t, err)
	require.NotNil(t, policy)
	read, err := m.terminalAccessHTTP(ctx, &MachineInput{MachineID: "1"})
	require.NoError(t, err)
	require.Equal(t, policy.Body.Data, read.Body.Data)
	metadata, err := m.getTerminalHTTP(ctx, &TerminalIDInput{ID: row.ID.String()})
	require.NoError(t, err)
	require.NotNil(t, metadata)
	owner, found, err := m.terminalOwner(ctx, row.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 9, owner.EntityID)
	_, found, err = m.terminalOwner(ctx, 99999)
	require.NoError(t, err)
	require.False(t, found)
	_, err = m.revokeTerminalHTTP(ctx, &terminalRevokeInput{TerminalIDInput: TerminalIDInput{ID: row.ID.String()}, Cookie: "browser", Origin: "https://console.invalid"})
	require.NoError(t, err)
	current, err := m.GetTerminal(ctx, 7, row.ID)
	require.NoError(t, err)
	require.Equal(t, "CLOSED", current.State)
	require.Equal(t, "USER", current.CloseReason)
	for _, id := range []string{"0", "invalid", "9999999999999999999999999"} {
		_, err = m.terminalAccessHTTP(ctx, &MachineInput{MachineID: id})
		require.Error(t, err)
		_, err = m.setTerminalAccessHTTP(ctx, &terminalAccessInput{MachineInput: MachineInput{MachineID: id}})
		require.Error(t, err)
		_, err = m.openTerminalHTTP(ctx, &terminalOpenInput{MachineInput: MachineInput{MachineID: id}})
		require.Error(t, err)
		_, err = m.getTerminalHTTP(ctx, &TerminalIDInput{ID: id})
		require.Error(t, err)
		_, err = m.revokeTerminalHTTP(ctx, &terminalRevokeInput{TerminalIDInput: TerminalIDInput{ID: id}})
		require.Error(t, err)
	}
	for _, version := range []string{"invalid", "0", "-1"} {
		in.IfMatch = version
		_, err = m.setTerminalAccessHTTP(ctx, in)
		require.Error(t, err)
	}
	in.IfMatch, in.Cookie = "2", "forged"
	_, err = m.setTerminalAccessHTTP(ctx, in)
	require.Error(t, err)
	_, err = m.openTerminalHTTP(ctx, &terminalOpenInput{MachineInput: MachineInput{MachineID: "1"}})
	require.Error(t, err)
	_, err = m.revokeTerminalHTTP(ctx, &terminalRevokeInput{TerminalIDInput: TerminalIDInput{ID: row.ID.String()}})
	require.Error(t, err)
	_, err = m.getTerminalHTTP(ctx, &TerminalIDInput{ID: "99999"})
	require.Error(t, err)
	for _, tc := range []struct {
		err    error
		status int
	}{{idempotency.ErrInvalidKey, 422}, {ErrTerminalAccessDisabled, 403}, {ErrTerminalClosed, 409}, {authn.ErrInvalidCredentials, 403}} {
		var status huma.StatusError
		require.ErrorAs(t, terminalHTTPError(ctx, tc.err), &status)
		require.Equal(t, tc.status, status.GetStatus())
	}
}

func TestTerminalPolicyStorageFailuresRollbackVersionAndAccess(t *testing.T) {
	for _, operation := range []string{"query", "create", "update"} {
		exercise := func(t *testing.T, failAt int) int {
			m, db, ctx, _, _ := terminalFixture(t)
			_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, err)
			before, err := m.GetTerminalAccess(ctx, 7, 1)
			require.NoError(t, err)
			count := 0
			injected := errors.New("remote access storage failure")
			hook := func(tx *gorm.DB) {
				if tx.Statement.Table != m.names.TerminalAccess.String() && tx.Statement.Table != m.names.Machines.String() {
					return
				}
				count++
				if count == failAt {
					tx.AddError(injected)
				}
			}
			switch operation {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("access_storage_failure", hook))
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("access_storage_failure", hook))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("access_storage_failure", hook))
			}
			bandwidth := 30000
			_, err = m.SetTerminalAccessOptions(ctx, 7, 1, 2, TerminalAccessOptions{Enabled: true, TunnelBandwidthKbps: &bandwidth}, "", "")
			require.NoError(t, db.Callback().Query().Remove("access_storage_failure"))
			require.NoError(t, db.Callback().Create().Remove("access_storage_failure"))
			require.NoError(t, db.Callback().Update().Remove("access_storage_failure"))
			if failAt == 0 {
				require.NoError(t, err)
				return count
			}
			require.ErrorIs(t, err, injected)
			after, err := m.GetTerminalAccess(ctx, 7, 1)
			require.NoError(t, err)
			require.Equal(t, before, after)
			return count
		}
		count := exercise(t, 0)
		for point := 1; point <= count; point++ {
			t.Run(fmt.Sprintf("%s/%d", operation, point), func(t *testing.T) { exercise(t, point) })
		}
	}
}

func TestTerminalIdempotencyReservationReplayCurrentIAMAndConflicts(t *testing.T) {
	m, db, ctx, existing, _ := terminalFixture(t)
	store := idempotency.NewModule(m.runtime)
	raw, err := db.DB()
	require.NoError(t, err)
	source := store.MigrationSource()
	source.Prefix = m.runtime.TablePrefix()
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	m.idempotency = store
	_, err = m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
	require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id = ?", existing.ID).Delete(existing).Error)
	proofCalls := 0
	m.revokeProof = func(_ context.Context, _ *gorm.DB, _ guid.ID, cookie, proof, purpose, target string) error {
		proofCalls++
		require.Equal(t, "browser", cookie)
		require.Equal(t, "fresh", proof)
		require.Equal(t, "terminal_interactive", purpose)
		require.Equal(t, "execution:91", target)
		return nil
	}
	digest, err := idempotency.HashRequest(http.MethodPost, "terminal-session.create", map[string]string{"machine_id": "1"}, struct {
		ExecutionID guid.ID
		Cols, Rows  int
	}{91, 80, 24})
	require.NoError(t, err)
	key := idempotency.Key{TenantID: 7, SubjectKind: "PRINCIPAL", SubjectID: 11, Operation: "terminal-session.create", Value: "terminal-once", RequestHash: digest}
	var receipt TerminalSession
	// Commit the exact C7 reservation/proof transaction, without native I/O.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := store.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			row, _, e := m.reserveTerminalOn(ctx, tx, 7, 1, 91, "browser", "fresh")
			if e != nil {
				return idempotency.Result{}, e
			}
			receipt = terminalView(row)
			data, e := json.Marshal(receipt)
			return idempotency.Result{StatusCode: 201, Body: data, ResourceKind: "terminal_session", ResourceID: row.ID}, e
		})
		return e
	}))
	require.Equal(t, "OPENING", receipt.State)
	require.Equal(t, 1, proofCalls)
	in := &terminalOpenInput{Key: key.Value, Cookie: "browser"}
	in.Body.ExecutionID, in.Body.Cols, in.Body.Rows, in.Body.Proof = 91, 80, 24, "consumed-proof"
	for range 2 {
		replayed, e := m.openTerminalReceipt(ctx, 1, in)
		require.NoError(t, e)
		expectedJSON, e := json.Marshal(receipt)
		require.NoError(t, e)
		actualJSON, e := json.Marshal(replayed)
		require.NoError(t, e)
		require.JSONEq(t, string(expectedJSON), string(actualJSON))
	}
	require.Equal(t, 1, proofCalls, "replay must not consume another proof or reserve another shell")
	var count int64
	require.NoError(t, db.Table(m.names.TerminalSessions.String()).Count(&count).Error)
	require.Equal(t, int64(1), count)
	in.Body.Cols = 81
	_, err = m.openTerminalReceipt(ctx, 1, in)
	require.ErrorIs(t, err, idempotency.ErrKeyReused)
	in.Body.Cols = 80
	// Exercise the platform's persisted in-flight branch through the real store.
	require.NoError(t, db.Table(source.Prefix+"platform_idempotency_keys").Where("key = ?", key.Value).Update("state", "IN_PROGRESS").Error)
	_, err = m.openTerminalReceipt(ctx, 1, in)
	require.ErrorIs(t, err, idempotency.ErrInProgress)
	require.NoError(t, db.Table(source.Prefix+"platform_idempotency_keys").Where("key = ?", key.Value).Update("state", "COMPLETED").Error)
	m.terminal.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
		return TerminalExecution{}, ErrMachineForbidden
	}
	_, err = m.openTerminalReceipt(ctx, 1, in)
	require.ErrorIs(t, err, ErrMachineForbidden, "cached receipt must not bypass revoked IAM")
	for _, tc := range []struct {
		err    error
		status int
		code   int
	}{{idempotency.ErrKeyReused, 422, 176425}, {idempotency.ErrInProgress, 409, 176409}} {
		response := terminalHTTPError(ctx, tc.err)
		var status huma.StatusError
		require.ErrorAs(t, response, &status)
		require.Equal(t, tc.status, status.GetStatus())
		data, e := json.Marshal(response)
		require.NoError(t, e)
		var envelope struct{ Code int }
		require.NoError(t, json.Unmarshal(data, &envelope))
		require.Equal(t, tc.code, envelope.Code)
	}
}

func TestTerminalChineseErrorTranslations(t *testing.T) {
	data, err := os.ReadFile("i18n/locales/zh-CN.json")
	require.NoError(t, err)
	var messages map[string]string
	require.NoError(t, json.Unmarshal(data, &messages))
	for _, key := range []string{"terminal_disabled", "terminal_closed", "terminal_reauthentication", "tunnel_quota"} {
		t.Run(key, func(t *testing.T) {
			value := messages[key]
			require.NotContains(t, value, "?")
			require.True(t, strings.ContainsFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }), "Chinese translation missing: %s", value)
		})
	}
}

func TestTerminalRevokeAndPolicyHTTPBindPathAndBody(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	got := ""
	version := ""
	huma.Register(api, huma.Operation{OperationID: "test.terminal-revoke", Method: http.MethodPost, Path: "/terminal-sessions/{terminal_session_id}/revoke"}, func(ctx context.Context, in *terminalRevokeInput) (*respx.Body[bool], error) {
		got = in.ID
		return respx.OK(ctx, true), nil
	})
	huma.Register(api, huma.Operation{OperationID: "test.terminal-policy", Method: http.MethodPut, Path: "/machines/{machine_id}/remote-access"}, func(ctx context.Context, in *terminalAccessInput) (*respx.Body[bool], error) {
		got = in.MachineID
		version = in.IfMatch
		require.True(t, in.Body.Enabled)
		return respx.OK(ctx, true), nil
	})
	req := httptest.NewRequest(http.MethodPost, "/terminal-sessions/501/revoke", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "501", got)
	req = httptest.NewRequest(http.MethodPut, "/machines/1/remote-access", strings.NewReader(`{"allow_remote_terminal":true,"reauth_proof":"fresh"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", "2")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "1", got)
	require.Equal(t, "2", version)
}

type terminalProjectHTTPAuthority struct{ tenantChecks, entityChecks *int }

func (a terminalProjectHTTPAuthority) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{TenantID: 7, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal}, nil
}
func (a terminalProjectHTTPAuthority) Check(_ context.Context, _ guid.ID, permission string, _ guid.ID) (bool, error) {
	*a.tenantChecks++
	return false, nil
}
func (a terminalProjectHTTPAuthority) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}
func (a terminalProjectHTTPAuthority) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
func (a terminalProjectHTTPAuthority) EntityConstraint(_ context.Context, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
	*a.entityChecks++
	return authz.DataConstraint{AllowAll: tenant == 7 && project == 9 && actor == 11 && permission == "terminal_interact"}, nil
}
func TestTerminalProjectScopedHTTPChecksProjectWithoutTenantPermission(t *testing.T) {
	m, _, _, _, _ := terminalFixture(t)
	services := module.NewServices()
	fm := files.NewModule(m.runtime, files.Config{LocalRoot: t.TempDir()})
	require.NoError(t, fm.ProvideServices(services))
	require.NoError(t, module.Provide[core.AuthorizationExplanation](services, m.terminal.permission))
	require.NoError(t, services.Seal())
	require.NoError(t, m.injectTerminal(services))
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "terminal", Verb: "interact", Scope: authz.ActionScopeTenant, DataScoped: true}, {Resource: "machine", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true}, {Resource: "machine_access", Verb: "manage", Scope: authz.ActionScopeTenant}}, nil))
	tenantChecks, entityChecks := 0, 0
	authority := terminalProjectHTTPAuthority{&tenantChecks, &entityChecks}
	m.registrar = authz.NewRegistrar(registry, authority, authority, authority)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, m.registerTerminalHTTP(api))
	require.NotNil(t, api.OpenAPI().Paths["/machines/{machine_id}/terminal-sessions"].Post.Responses["429"])
	m.terminal.transport.Available = func(guid.ID, guid.ID) bool { return true }
	broker := m.terminalBroker
	secondAPI := humax.New(chi.NewRouter(), humax.Config{Name: "test-second-router", Version: "1"})
	require.NoError(t, m.registerTerminalHTTP(secondAPI))
	require.Same(t, broker, m.terminalBroker)
	require.True(t, m.terminal.transport.Available(7, 1), "repeat registration must preserve the bound transport")
	require.ErrorIs(t, m.BindTerminalTransport(m.terminal.transport), ErrInvalidArgument, "explicit duplicate binding remains invalid")
	m.revokeBrowser.ValidateCSRF = func(string, string, string) error { return nil }
	checked := false
	m.terminal.execution = func(ctx context.Context, _ *gorm.DB, tenant, id guid.ID, permission string) (TerminalExecution, error) {
		checked = true
		if tenant != 7 || id != 91 || authn.PrincipalIDFromContext(ctx) != 11 {
			return TerminalExecution{}, ErrMachineForbidden
		}
		return TerminalExecution{ID: 91, ProjectID: 9, MachineID: 1, Workspace: "/actual/execution/worktree", DispatchEpoch: 4}, nil
	}
	m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	// Get/revoke bind the session's project; this caller has no tenant permission.
	response := fleetHTTPRequest(router, http.MethodGet, "/terminal-sessions/501", "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.True(t, checked)
	require.Equal(t, 0, tenantChecks)
	require.Positive(t, entityChecks)
	// The existing active-session fence is reached after current project authority;
	// a tenant-level permission guard would reject this request before the domain.
	checked = false
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/terminal-sessions", `{"execution_id":"91","cols":80,"rows":24,"reauth_proof":"fresh"}`, map[string]string{"Cookie": "browser"})
	require.Equal(t, 403, response.Code, response.Body.String())
	// Enable machine access via the real domain policy; duplicate open then yields409.
	m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	_, err = m.SetTerminalAccess(terminalOwnerContext(context.Background(), 7, 11), 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
	checked = false
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/terminal-sessions", `{"execution_id":"91","cols":80,"rows":24,"reauth_proof":"fresh"}`, map[string]string{"Cookie": "browser"})
	require.Equal(t, 409, response.Code, response.Body.String())
	require.True(t, checked)
	require.Equal(t, 0, tenantChecks)
}

func TestTerminalOpenHTTPAndReceiptUseNativeHandshakeAndDurableRecording(t *testing.T) {
	for _, key := range []string{"", "native-receipt"} {
		t.Run("key="+key, func(t *testing.T) {
			m, db, ctx, old, _ := terminalFixture(t)
			bindTerminalRecordingFixture(t, m, db, ctx)
			_, e := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, e)
			require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id=?", old.ID).Delete(old).Error)
			store := idempotency.NewModule(m.runtime)
			raw, e := db.DB()
			require.NoError(t, e)
			source := store.MigrationSource()
			source.Prefix = m.runtime.TablePrefix()
			require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
			services := module.NewServices()
			require.NoError(t, module.Provide[guid.Source](services, m.ids))
			require.NoError(t, services.Seal())
			require.NoError(t, store.InjectServices(services))
			m.idempotency = store
			m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
			b := newTerminalBroker()
			m.terminalBroker = b
			c, e := b.connect(7, 1, old.RunnerSessionID, old.ConnEpoch)
			require.NoError(t, e)
			defer b.disconnect(c)
			require.NoError(t, m.BindTerminalTransport(TerminalTransport{Available: b.available, Open: m.openTerminalStream, Close: func(_ context.Context, s TerminalSession, reason string) error { return b.close(s, reason) }}))
			ack := make(chan error, 1)
			go func() {
				select {
				case frame := <-c.frames:
					id, e := guid.Parse(frame.SessionID)
					if e != nil {
						ack <- e
						return
					}
					stream, e := b.lookup(id)
					if e != nil {
						ack <- e
						return
					}
					stream.opened <- terminalFrame{Kind: "opened", Sandboxed: true}
					ack <- nil
				case <-time.After(time.Second):
					ack <- errors.New("native open not sent")
				}
			}()
			m.revokeBrowser.ValidateCSRF = func(method, origin, cookie string) error {
				require.Equal(t, http.MethodPost, method)
				require.Equal(t, "https://console.test", origin)
				require.Equal(t, "browser", cookie)
				return nil
			}
			in := &terminalOpenInput{MachineInput: MachineInput{MachineID: "1"}, Cookie: "browser", Origin: "https://console.test", Key: key}
			in.Body.ExecutionID = 91
			in.Body.Cols = 80
			in.Body.Rows = 24
			in.Body.Proof = "fresh"
			response, e := m.openTerminalHTTP(ctx, in)
			require.NoError(t, e)
			require.NotNil(t, response)
			require.NoError(t, <-ack)
			var rows []struct {
				ID    guid.ID
				State string
			}
			require.NoError(t, db.Table(m.names.TerminalSessions.String()).Find(&rows).Error)
			require.Len(t, rows, 1)
			require.Equal(t, "OPEN", rows[0].State)
			require.NoError(t, m.RecordTerminal(ctx, 7, rows[0].ID, "i", []byte("echo native\r")))
			closed, e := m.CloseTerminal(ctx, 7, rows[0].ID, "USER")
			require.NoError(t, e)
			require.NotNil(t, closed.RecordingFileID)
			require.Len(t, closed.RecordingSHA256, 64)
			<-c.frames
			if key != "" {
				replay, e := m.openTerminalHTTP(ctx, in)
				require.NoError(t, e)
				require.Equal(t, response.Body.Data, replay.Body.Data)
				require.Empty(t, c.frames)
			}
			require.Zero(t, m.tunnelBudget(7, 1).streams)
		})
	}
}

func TestTerminalReceiptMissingPrerequisitesAndCurrentSourcesReturnNoShell(t *testing.T) {
	for _, fault := range []string{"store", "execution", "dimensions", "writer", "transport", "proof", "wrong_machine"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, row, _ := terminalFixture(t)
			store := idempotency.NewModule(m.runtime)
			raw, e := db.DB()
			require.NoError(t, e)
			source := store.MigrationSource()
			source.Prefix = m.runtime.TablePrefix()
			require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
			services := module.NewServices()
			require.NoError(t, module.Provide[guid.Source](services, m.ids))
			require.NoError(t, services.Seal())
			require.NoError(t, store.InjectServices(services))
			m.idempotency = store
			in := &terminalOpenInput{Key: "unavailable", Cookie: "browser"}
			in.Body.ExecutionID = 91
			in.Body.Cols = 80
			in.Body.Rows = 24
			switch fault {
			case "store":
				m.idempotency = nil
			case "execution":
				m.terminal.execution = nil
			case "dimensions":
				in.Body.Cols = 0
			case "writer":
				m.runtime = nil
			case "proof":
				m.revokeProof = nil
			case "wrong_machine":
				m.terminal.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
					return TerminalExecution{MachineID: 99}, nil
				}
			}
			out, e := m.openTerminalReceipt(ctx, 1, in)
			require.Error(t, e)
			require.Zero(t, out.ID)
			var count int64
			require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id<>?", row.ID).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
