package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestPreviewCreationAndGrantStorageFailureNeverLeavesPartialSession(t *testing.T) {
	for _, command := range []string{"create", "grant"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f := executionHarness(t)
				configurePreviewFixture(t, f)
				v := f.create(t)
				f.pull(t, v.ID)
				var session PreviewSessionView
				if command == "grant" {
					var e error
					session, e = f.m.CreatePreview(f.human, 7, v.ID, 3000)
					require.NoError(t, e)
				}
				return f, func(*gorm.DB) error {
					if command == "grant" {
						_, e := f.m.IssuePreviewAccess(f.human, 7, session.ID, "/")
						return e
					}
					out, e := f.m.CreatePreview(f.human, 7, v.ID, 3000)
					if e == nil {
						require.Equal(t, "READY", out.State)
					}
					return e
				}
			}, false, true)
		})
	}
}

func TestPreviewChineseErrorTranslations(t *testing.T) {
	data, err := os.ReadFile("i18n/locales/zh-CN.json")
	require.NoError(t, err)
	var messages map[string]string
	require.NoError(t, json.Unmarshal(data, &messages))
	for _, key := range []string{"preview_port_not_owned", "preview_access_disabled", "preview_unavailable", "preview_quota"} {
		t.Run(key, func(t *testing.T) {
			value := messages[key]
			require.NotContains(t, value, "?")
			require.True(t, strings.ContainsFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }), "Chinese translation missing: %s", value)
		})
	}
}

func configurePreviewFixture(t *testing.T, f *executionFixture) {
	t.Helper()
	f.m.preview.runtime = &fleet.PreviewRuntime{
		Config: func() fleet.PreviewConfig {
			return fleet.PreviewConfig{Domain: "preview.example.net", ConsoleOrigin: "https://console.example.com", MaxSessionMinutes: 480}
		},
		Available: func(tenant, machine guid.ID) bool { return tenant == 7 && machine == 1 && f.sessionValid },
		MachineOn: func(_ context.Context, _ *gorm.DB, tenant, machine guid.ID) (fleet.PreviewMachine, error) {
			require.Equal(t, guid.ID(7), tenant)
			require.Equal(t, guid.ID(1), machine)
			return fleet.PreviewMachine{RunnerSessionID: f.fence.SessionID, ConnEpoch: f.fence.ConnEpoch, Enabled: true, State: f.machineState, TunnelMaxStreams: 256}, nil
		},
		Ports: func(_ context.Context, g fleet.PreviewGrant) ([]int, error) {
			require.Equal(t, "/registered/workspace", g.Workspace)
			if g.Port == 8080 {
				return nil, nil
			}
			return []int{g.Port}, nil
		},
		ResetSession: func(context.Context, guid.ID, string) error { return nil },
	}
}
func previewCodeFromURL(t *testing.T, value string) (string, string) {
	t.Helper()
	u, err := url.Parse(value)
	require.NoError(t, err)
	return strings.Split(u.Host, ".")[0], u.Query().Get("code")
}
func previewCookieRequest(t *testing.T, value string) string {
	t.Helper()
	response := &http.Response{Header: http.Header{"Set-Cookie": []string{value}}}
	cookies := response.Cookies()
	require.Len(t, cookies, 1)
	require.True(t, cookies[0].Secure)
	require.True(t, cookies[0].HttpOnly)
	return cookies[0].Name + "=" + cookies[0].Value
}
func TestPreviewOwnedPortLifecycleAndCurrentAccess(t *testing.T) {
	f := executionHarness(t)
	configurePreviewFixture(t, f)
	execution := f.create(t)
	_, err := f.m.CreatePreview(f.human, 7, execution.ID, 8080)
	require.ErrorIs(t, err, ErrPreviewPortNotOwned)
	session, err := f.m.CreatePreview(f.human, 7, execution.ID, 5173)
	require.NoError(t, err)
	require.Equal(t, "READY", session.State)
	duplicate, err := f.m.CreatePreview(f.human, 7, execution.ID, 5173)
	require.NoError(t, err)
	require.Equal(t, session.ID, duplicate.ID)
	require.True(t, duplicate.Reused)
	encoded, err := json.Marshal(session)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "label")
	access, err := f.m.IssuePreviewAccess(f.human, 7, session.ID, "/app?tab=1")
	require.NoError(t, err)
	label, code := previewCodeFromURL(t, access.URL)
	require.Len(t, label, 26)
	require.Len(t, code, 43)
	_, _, _, err = f.m.exchangePreviewCode(t.Context(), strings.Repeat("a", 26), code)
	require.ErrorIs(t, err, ErrForbidden)
	setCookie, path, expires, err := f.m.exchangePreviewCode(t.Context(), label, code)
	require.NoError(t, err)
	require.Equal(t, "/app?tab=1", path)
	require.WithinDuration(t, time.Now().Add(15*time.Minute), expires, 2*time.Second)
	cookie := previewCookieRequest(t, setCookie)
	_, _, _, err = f.m.exchangePreviewCode(t.Context(), label, code)
	require.ErrorIs(t, err, ErrForbidden)
	var deniedCount int64
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "execution.preview_access_denied").Count(&deniedCount).Error)
	require.EqualValues(t, 2, deniedCount, "wrong-label and replay denials must be audited without token bytes")
	grant, err := f.m.authorizePreview(t.Context(), label, cookie, "GET")
	require.NoError(t, err)
	require.Equal(t, session.ID, grant.SessionID)
	require.Equal(t, 256, grant.TunnelMaxStreams)
	require.Contains(t, grant.Cookie, "HttpOnly")
	q := query.BindPreviewAccess(f.db, f.m.names)
	stored, err := q.WithContext(t.Context()).First()
	require.NoError(t, err)
	require.NotEqual(t, code, stored.CodeHash)
	require.NotContains(t, setCookie, *stored.CookieHash)
	f.authorized = false
	_, err = f.m.authorizePreview(t.Context(), label, cookie, "GET")
	require.ErrorIs(t, err, ErrForbidden)
	require.ErrorIs(t, f.m.touchPreview(t.Context(), session.ID), ErrForbidden)
	f.authorized = true
	f.humanActive = false
	_, err = f.m.authorizePreview(t.Context(), label, cookie, "GET")
	require.ErrorIs(t, err, ErrForbidden)
	f.humanActive = true
	f.fence.ConnEpoch++
	_, err = f.m.authorizePreview(t.Context(), label, cookie, "GET")
	require.ErrorIs(t, err, ErrFenced)
	f.fence.ConnEpoch--
	stopped, err := f.m.StopPreview(f.human, 7, session.ID)
	require.NoError(t, err)
	require.Equal(t, "STOPPED", stopped.State)
	_, err = f.m.authorizePreview(t.Context(), label, cookie, "GET")
	require.ErrorIs(t, err, ErrConflict)
}
func TestPreviewQuotaExpiryAndAtomicOutbox(t *testing.T) {
	f := executionHarness(t)
	configurePreviewFixture(t, f)
	execution := f.create(t)
	f.pub.fail = true
	_, err := f.m.CreatePreview(f.human, 7, execution.ID, 5173)
	require.Error(t, err)
	q := query.BindPreviewSessions(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	f.pub.fail = false
	sessions := []PreviewSessionView{}
	for _, port := range []int{5173, 5174, 5175} {
		session, e := f.m.CreatePreview(f.human, 7, execution.ID, port)
		require.NoError(t, e)
		sessions = append(sessions, session)
	}
	_, err = f.m.CreatePreview(f.human, 7, execution.ID, 5176)
	require.ErrorIs(t, err, ErrPreviewQuota)
	grant, err := f.m.IssuePreviewAccess(f.human, 7, sessions[0].ID, "/")
	require.NoError(t, err)
	label, code := previewCodeFromURL(t, grant.URL)
	access := query.BindPreviewAccess(f.db, f.m.names)
	_, err = access.WithContext(t.Context()).Where(access.CodeHash.Eq(previewDigest(code))).Update(access.CodeExpiresAt, time.Now().Add(-time.Second))
	require.NoError(t, err)
	_, _, _, err = f.m.exchangePreviewCode(t.Context(), label, code)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(sessions[0].ID)).Update(q.LastAccessAt, time.Now().Add(-31*time.Minute))
	require.NoError(t, err)
	require.NoError(t, f.m.expirePreviews(t.Context(), f.db, 7, time.Now()))
	expired, err := f.m.GetPreview(f.human, 7, sessions[0].ID)
	require.NoError(t, err)
	require.Equal(t, "STOPPED", expired.State)
	require.Equal(t, "IDLE", expired.StopReason)
}

func TestPreviewNativeProbeRunsWithoutCallerTransactionAndRechecksFence(t *testing.T) {
	f := executionHarness(t)
	configurePreviewFixture(t, f)
	execution := f.create(t)
	raw, err := f.db.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	probes := 0
	f.m.preview.runtime.Ports = func(ctx context.Context, g fleet.PreviewGrant) ([]int, error) {
		probes++
		// Real broker admission takes its own writer transaction. With a one-slot
		// writer pool this deadlocks if domain authorization still owns the slot.
		err := f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Exec("INSERT INTO effects(kind) VALUES (?)", "native-port.probe").Error
		})
		if err != nil {
			return nil, err
		}
		return []int{5173}, nil
	}
	ctx, cancel := context.WithTimeout(f.human, 2*time.Second)
	defer cancel()
	ports, err := f.m.OfferedPreviewPorts(ctx, 7, execution.ID)
	require.NoError(t, err)
	require.Equal(t, 5173, ports[0].Port)
	_, err = f.m.CreatePreview(ctx, 7, execution.ID, 5173)
	require.NoError(t, err)
	require.Equal(t, 2, probes)
	f.m.preview.runtime.Ports = func(context.Context, fleet.PreviewGrant) ([]int, error) { f.fence.ConnEpoch++; return []int{5174}, nil }
	_, err = f.m.CreatePreview(ctx, 7, execution.ID, 5174)
	require.ErrorIs(t, err, ErrFenced)
	q := query.BindPreviewSessions(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
func TestPreviewLongLivedStreamRechecksActualReaderRatherThanOpener(t *testing.T) {
	f := executionHarness(t)
	configurePreviewFixture(t, f)
	execution := f.create(t)
	session, err := f.m.CreatePreview(f.human, 7, execution.ID, 5173)
	require.NoError(t, err)
	reader := previewActor(f.human, 7, 22)
	access, err := f.m.IssuePreviewAccess(reader, 7, session.ID, "/")
	require.NoError(t, err)
	label, code := previewCodeFromURL(t, access.URL)
	cookie, _, _, err := f.m.exchangePreviewCode(t.Context(), label, code)
	require.NoError(t, err)
	grant, err := f.m.authorizePreview(t.Context(), label, previewCookieRequest(t, cookie), "GET")
	require.NoError(t, err)
	require.Equal(t, guid.ID(22), grant.PrincipalID)
	require.Positive(t, int64(grant.AccessID))
	require.NoError(t, f.m.revalidatePreview(t.Context(), grant))
	original := f.m.policy
	f.m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if actor == 22 {
			return authz.DataConstraint{}, nil
		}
		return original(ctx, tx, tenant, project, permission, actor)
	}
	require.NoError(t, f.m.touchPreview(t.Context(), session.ID), "the opener retains permission")
	require.ErrorIs(t, f.m.revalidatePreview(t.Context(), grant), ErrForbidden, "the revoked reader's existing stream must terminate")
}

func TestPreviewMachineDisableResetsEveryNativeStreamWhenMetadataPublicationFails(t *testing.T) {
	f := executionHarness(t)
	configurePreviewFixture(t, f)
	execution := f.create(t)
	ids := []guid.ID{}
	for _, port := range []int{5173, 5174} {
		session, err := f.m.CreatePreview(f.human, 7, execution.ID, port)
		require.NoError(t, err)
		ids = append(ids, session.ID)
	}
	reset := []guid.ID{}
	f.m.preview.runtime.ResetSession = func(_ context.Context, id guid.ID, reason string) error {
		require.Equal(t, "ACCESS_DISABLED", reason)
		reset = append(reset, id)
		return nil
	}
	f.pub.fail = true
	require.Error(t, f.m.stopMachinePreviews(t.Context(), 7, 1, "ACCESS_DISABLED"))
	require.ElementsMatch(t, ids, reset, "a failed metadata write must not leave other native streams open")
}

func TestPreviewEveryStorageFailurePreservesSessionAndPrivateAccessGrant(t *testing.T) {
	for _, command := range []string{"create", "get", "list", "stop", "issue", "exchange", "authorize", "touch"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f := executionHarness(t)
				configurePreviewFixture(t, f)
				execution := f.create(t)
				var session PreviewSessionView
				var label, code, cookie string
				if command != "create" {
					var e error
					session, e = f.m.CreatePreview(f.human, 7, execution.ID, 5173)
					require.NoError(t, e)
				}
				if command == "exchange" || command == "authorize" {
					access, e := f.m.IssuePreviewAccess(f.human, 7, session.ID, "/app")
					require.NoError(t, e)
					label, code = previewCodeFromURL(t, access.URL)
					if command == "authorize" {
						value, _, _, e := f.m.exchangePreviewCode(t.Context(), label, code)
						require.NoError(t, e)
						cookie = previewCookieRequest(t, value)
					}
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.PreviewSessions.String(), f.m.names.PreviewAccess.String(), f.m.names.Executions.String()} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("preview storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("preview_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("preview_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("preview_transaction_failure", hook))
				}
				var err error
				switch command {
				case "create":
					_, err = f.m.CreatePreview(f.human, 7, execution.ID, 5173)
				case "get":
					_, err = f.m.GetPreview(f.human, 7, session.ID)
				case "list":
					_, err = f.m.ListPreviews(f.human, 7, execution.ID)
				case "stop":
					_, err = f.m.StopPreview(f.human, 7, session.ID)
				case "issue":
					_, err = f.m.IssuePreviewAccess(f.human, 7, session.ID, "/app")
				case "exchange":
					var value string
					value, _, _, err = f.m.exchangePreviewCode(t.Context(), label, code)
					if failAt > 0 {
						require.Empty(t, value)
					}
				case "authorize":
					_, err = f.m.authorizePreview(t.Context(), label, cookie, "GET")
				default:
					err = f.m.touchPreview(t.Context(), session.ID)
				}
				require.NoError(t, f.db.Callback().Query().Remove("preview_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("preview_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("preview_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				if kind == "query" && failAt == 1 && (command == "exchange" || command == "authorize") {
					require.ErrorIs(t, err, ErrForbidden)
				} else {
					require.ErrorIs(t, err, injected)
				}
				require.Equal(t, before, snapshot())
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func TestPreviewCurrentSourcesAndPrivateGrantFailuresLeaveNoSessionOrAccess(t *testing.T) {
	for _, fault := range []string{"writer", "runtime_missing", "machine_source", "machine_disabled", "machine_offline", "machine_session", "transport", "ports_source", "ports_forbidden", "principal_source", "policy_source", "id_error", "id_zero", "invalid_port"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			configurePreviewFixture(t, f)
			execution := f.create(t)
			injected := errors.New("preview current source unavailable")
			port := 5173
			switch fault {
			case "writer":
				f.m.runtime = nil
			case "runtime_missing":
				f.m.preview.runtime = nil
			case "machine_source", "machine_disabled", "machine_offline", "machine_session":
				original := f.m.preview.runtime.MachineOn
				f.m.preview.runtime.MachineOn = func(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID) (fleet.PreviewMachine, error) {
					r, e := original(ctx, tx, tenant, machine)
					switch fault {
					case "machine_source":
						e = injected
					case "machine_disabled":
						r.Enabled = false
					case "machine_offline":
						r.State = "OFFLINE"
					case "machine_session":
						r.RunnerSessionID = 0
					}
					return r, e
				}
			case "transport":
				f.sessionValid = false
			case "ports_source":
				f.m.preview.runtime.Ports = func(context.Context, fleet.PreviewGrant) ([]int, error) { return nil, injected }
			case "ports_forbidden":
				f.m.preview.runtime.Ports = func(context.Context, fleet.PreviewGrant) ([]int, error) { return nil, fleet.ErrMachineForbidden }
			case "principal_source":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "policy_source":
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
			case "id_error":
				f.m.ids = failedExecutionIDs{err: injected}
			case "id_zero":
				f.m.ids = failedExecutionIDs{}
			case "invalid_port":
				port = 65536
			}
			out, e := f.m.CreatePreview(f.human, 7, execution.ID, port)
			require.Error(t, e)
			require.Zero(t, out.ID)
			for _, table := range []string{f.m.names.PreviewSessions.String(), f.m.names.PreviewAccess.String()} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestPreviewPathModeCookiesAreScopedAndCannotOutliveSession(t *testing.T) {
	f := executionHarness(t)
	configurePreviewFixture(t, f)
	f.m.preview.runtime.Config = func() fleet.PreviewConfig {
		return fleet.PreviewConfig{Domain: "preview.example.net", ConsoleOrigin: "https://console.example.com", Mode: "PATH", MaxSessionMinutes: 1}
	}
	v := f.create(t)
	s, e := f.m.CreatePreview(f.human, 7, v.ID, 5173)
	require.NoError(t, e)
	require.Equal(t, "PATH", s.URLMode)
	require.True(t, s.PathModeWarning)
	access, e := f.m.IssuePreviewAccess(f.human, 7, s.ID, "/app")
	require.NoError(t, e)
	u, e := url.Parse(access.URL)
	require.NoError(t, e)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	require.Len(t, parts, 3)
	label := parts[1]
	code := u.Query().Get("code")
	cookie, path, expires, e := f.m.exchangePreviewCode(t.Context(), label, code)
	require.NoError(t, e)
	require.Equal(t, "/app", path)
	require.WithinDuration(t, s.ExpiresAt, expires, time.Millisecond)
	require.Contains(t, cookie, "__Secure-preview=")
	require.Contains(t, cookie, "Path=/p/"+label+"/")
	grant, e := f.m.authorizePreview(t.Context(), label, previewCookieRequest(t, cookie), http.MethodGet)
	require.NoError(t, e)
	require.Contains(t, grant.Cookie, "Path=/p/"+label+"/")
	require.NoError(t, f.m.revalidatePreview(t.Context(), grant))
	for _, fault := range []string{"empty_method", "short_label", "wrong_label", "missing_cookie", "short_cookie"} {
		t.Run(fault, func(t *testing.T) {
			l, c, method := label, previewCookieRequest(t, cookie), http.MethodGet
			switch fault {
			case "empty_method":
				method = ""
			case "short_label":
				l = "a"
			case "wrong_label":
				l = strings.Repeat("a", 26)
			case "missing_cookie":
				c = ""
			case "short_cookie":
				c = "__Secure-preview=short"
			}
			_, e := f.m.authorizePreview(t.Context(), l, c, method)
			require.ErrorIs(t, e, ErrForbidden)
		})
	}
	for _, fault := range []string{"access", "principal", "machine", "epoch", "session", "expired_cookie"} {
		t.Run(fault, func(t *testing.T) {
			bad := grant
			switch fault {
			case "access":
				bad.AccessID++
			case "principal":
				bad.PrincipalID++
			case "machine":
				bad.MachineID++
			case "epoch":
				bad.ConnEpoch++
			case "session":
				bad.SessionID++
			case "expired_cookie":
				q := query.BindPreviewAccess(f.db, f.m.names)
				_, e := q.WithContext(t.Context()).Where(q.ID.Eq(grant.AccessID)).Update(q.CookieExpiresAt, time.Now().Add(-time.Second))
				require.NoError(t, e)
			}
			require.Error(t, f.m.revalidatePreview(t.Context(), bad))
		})
	}
}
func TestPreviewExpiryStopsOnlyActualExpiredOrRevokedRuntimeAndResetsTransport(t *testing.T) {
	for _, fault := range []string{"none", "lifetime", "idle", "disabled", "offline", "unavailable", "fenced", "permission", "source_error", "reset_error"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			configurePreviewFixture(t, f)
			v := f.create(t)
			s, e := f.m.CreatePreview(f.human, 7, v.ID, 5173)
			require.NoError(t, e)
			failure := errors.New("preview current source failed")
			now := time.Now()
			reason := ""
			resets := 0
			f.m.preview.runtime.ResetSession = func(_ context.Context, id guid.ID, r string) error {
				resets++
				require.Equal(t, s.ID, id)
				require.NotEmpty(t, r)
				if fault == "reset_error" {
					return failure
				}
				return nil
			}
			switch fault {
			case "lifetime":
				now = s.ExpiresAt.Add(time.Second)
				reason = "MAX_LIFETIME"
			case "idle", "reset_error":
				require.NoError(t, f.db.Table(f.m.names.PreviewSessions.String()).Where("id=?", s.ID).Update("last_access_at", now.Add(-31*time.Minute)).Error)
				reason = "IDLE"
			case "disabled", "offline", "unavailable", "source_error":
				old := f.m.preview.runtime.MachineOn
				f.m.preview.runtime.MachineOn = func(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID) (fleet.PreviewMachine, error) {
					r, e := old(ctx, tx, tenant, machine)
					switch fault {
					case "disabled":
						r.Enabled = false
					case "offline":
						r.State = "OFFLINE"
					case "unavailable":
						r.RunnerSessionID = 0
					case "source_error":
						e = failure
					}
					return r, e
				}
				reason = map[string]string{"disabled": "ACCESS_DISABLED", "offline": "MACHINE_OFFLINE", "unavailable": "MACHINE_OFFLINE"}[fault]
			case "fenced":
				f.fence.ConnEpoch++
				reason = "EXECUTION_ENDED"
			case "permission":
				f.authorized = false
				reason = "ACCESS_DISABLED"
			}
			e = f.m.expirePreviews(f.human, f.db, 7, now)
			if fault == "source_error" || fault == "reset_error" {
				require.ErrorIs(t, e, failure)
			} else {
				require.NoError(t, e)
			}
			var stored PreviewSessionView
			f.authorized = true
			stored, e = f.m.GetPreview(f.human, 7, s.ID)
			require.NoError(t, e)
			if reason == "" {
				require.Equal(t, "READY", stored.State)
				require.Zero(t, resets)
			} else {
				require.Equal(t, "STOPPED", stored.State)
				require.Equal(t, reason, stored.StopReason)
				require.Equal(t, 1, resets)
			}
		})
	}
}

func TestBindPreviewRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindPreview(module.NewServices()), module.ErrServicesProviding)
}

func TestPreviewReplacementAndAccessFailuresPreserveSessions(t *testing.T) {
	for _, reason := range []string{"MAX_LIFETIME", "IDLE", "EXECUTION_ENDED"} {
		t.Run(reason, func(t *testing.T) {
			f := executionHarness(t)
			configurePreviewFixture(t, f)
			config := f.m.preview.runtime.Config()
			config.MaxSessionMinutes = 0
			f.m.preview.runtime.Config = func() fleet.PreviewConfig { return config }
			v := f.create(t)
			old, err := f.m.CreatePreview(f.human, 7, v.ID, 3000)
			require.NoError(t, err)
			require.WithinDuration(t, old.LastAccessAt.Add(8*time.Hour), old.ExpiresAt, time.Millisecond)
			field, value := "expires_at", any(time.Now().Add(-time.Second))
			switch reason {
			case "IDLE":
				field, value = "last_access_at", time.Now().Add(-31*time.Minute)
			case "EXECUTION_ENDED":
				field, value = "conn_epoch", f.fence.ConnEpoch+1
			}
			require.NoError(t, f.db.Table(f.m.names.PreviewSessions.String()).Where("id=?", old.ID).Update(field, value).Error)
			replacement, err := f.m.CreatePreview(f.human, 7, v.ID, 3000)
			require.NoError(t, err)
			require.NotEqual(t, old.ID, replacement.ID)
			stored, err := f.m.GetPreview(f.human, 7, old.ID)
			require.NoError(t, err)
			require.Equal(t, "STOPPED", stored.State)
			require.Equal(t, reason, stored.StopReason)
			_, err = f.m.StopPreview(f.human, 7, old.ID)
			require.NoError(t, err)
			_, err = f.m.GetPreview(f.human, 8, replacement.ID)
			require.ErrorIs(t, err, ErrNotFound)
			_, err = f.m.GetPreview(f.human, 7, replacement.ID+1000)
			require.ErrorIs(t, err, ErrNotFound)
			_, err = f.m.StopPreview(f.human, 8, replacement.ID)
			require.ErrorIs(t, err, ErrNotFound)
			_, err = f.m.IssuePreviewAccess(f.human, 8, replacement.ID, "/")
			require.ErrorIs(t, err, ErrNotFound)
			_, err = f.m.IssuePreviewAccess(f.human, 7, replacement.ID, "//outside.example")
			require.ErrorIs(t, err, ErrInvalidArgument)
			failure := errors.New("preview access identity failed")
			f.m.ids = failedExecutionIDs{err: failure}
			_, err = f.m.IssuePreviewAccess(f.human, 7, replacement.ID, "/")
			require.ErrorIs(t, err, failure)
			f.m.ids = failedExecutionIDs{}
			_, err = f.m.IssuePreviewAccess(f.human, 7, replacement.ID, "/")
			require.ErrorIs(t, err, ErrUnavailable)
			var accesses int64
			require.NoError(t, f.db.Table(f.m.names.PreviewAccess.String()).Where("session_id=?", replacement.ID).Count(&accesses).Error)
			require.Zero(t, accesses)
			_, _, _, err = f.m.exchangePreviewCode(t.Context(), "short", "invalid")
			require.ErrorIs(t, err, ErrForbidden)
			require.ErrorIs(t, f.m.revalidatePreview(t.Context(), fleet.PreviewGrant{}), ErrForbidden)
			f.m.preview.runtime = nil
			_, err = f.m.IssuePreviewAccess(f.human, 7, replacement.ID, "/")
			require.ErrorIs(t, err, ErrPreviewUnavailable)
		})
	}
}
func TestOfferedPreviewPortsRechecksRuntimeAfterDiscovery(t *testing.T) {
	for _, fault := range []string{"source", "revoked", "fenced", "invalid"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			configurePreviewFixture(t, f)
			v := f.create(t)
			failure := errors.New("port discovery failed")
			f.m.preview.runtime.Ports = func(context.Context, fleet.PreviewGrant) ([]int, error) {
				switch fault {
				case "source":
					return nil, failure
				case "revoked":
					f.authorized = false
				case "fenced":
					f.fence.ConnEpoch++
				}
				return []int{0}, nil
			}
			ports, err := f.m.OfferedPreviewPorts(f.human, 7, v.ID)
			require.Empty(t, ports)
			switch fault {
			case "source":
				require.ErrorIs(t, err, failure)
			case "revoked":
				require.ErrorIs(t, err, ErrForbidden)
			case "fenced":
				require.ErrorIs(t, err, ErrFenced)
			case "invalid":
				require.ErrorIs(t, err, ErrInvalidArgument)
			}
		})
	}
}

func TestPreviewCommandsRequireWritableRuntime(t *testing.T) {
	f := executionHarness(t)
	configurePreviewFixture(t, f)
	f.m.runtime = nil
	_, err := f.m.IssuePreviewAccess(f.human, 7, 1, "/")
	require.ErrorIs(t, err, ErrUnavailable)
	_, _, _, err = f.m.exchangePreviewCode(t.Context(), strings.Repeat("a", 26), strings.Repeat("b", 43))
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = f.m.authorizePreview(t.Context(), strings.Repeat("a", 26), "__Host-preview="+strings.Repeat("b", 43), http.MethodGet)
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorIs(t, f.m.stopMachinePreviews(t.Context(), 7, 1, "USER"), ErrUnavailable)
	require.ErrorIs(t, f.m.revalidatePreview(t.Context(), fleet.PreviewGrant{SessionID: 1, AccessID: 2, PrincipalID: 11}), ErrUnavailable)
}

func TestPreviewAuditFailureRollsBackSessionCreation(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			f := executionHarness(t)
			configurePreviewFixture(t, f)
			v := f.create(t)
			failure := errors.New("preview audit unavailable")
			f.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			if missing {
				f.m.audit = nil
			}
			_, err := f.m.CreatePreview(f.human, 7, v.ID, 3000)
			if missing {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.ErrorIs(t, err, failure)
			}
			var count int64
			require.NoError(t, f.db.Table(f.m.names.PreviewSessions.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
