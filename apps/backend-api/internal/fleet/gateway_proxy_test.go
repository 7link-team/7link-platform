package fleet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGatewayAdmissionAndSettlementStorageFailuresRollbackLedgerAndBudget(t *testing.T) {
	for _, command := range []string{"admit", "settle", "current"} {
		for _, operation := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				m, db, ctx, _, _ := enrollmentHarness(t)
				in := gatewayWrite()
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 0, in); return err }))
				require.NoError(t, db.Table(m.names.GatewayConfigs.String()).Where("tenant_id=?", 7).Update("enabled", true).Error)
				require.NoError(t, db.Exec("CREATE TABLE gateway_budget_effects(kind TEXT NOT NULL)").Error)
				m.gatewayBudgets.ReserveOn = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, _ int64, _ *int64) error {
					return tx.Exec("INSERT INTO gateway_budget_effects VALUES ('reserved')").Error
				}
				m.gatewaySettlement = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, _ int64) error {
					return tx.Exec("INSERT INTO gateway_budget_effects VALUES ('settled')").Error
				}
				input := GatewayProxyInput{ProjectID: 20, MaxInputTokens: 10, MaxOutputTokens: 10, Request: json.RawMessage(`{"input":"hello"}`)}
				var route gatewayRoute
				if command != "admit" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						route, err = m.prepareGatewayProxyOn(ctx, tx, 7, input)
						return err
					}))
				}
				var beforeEffects int64
				require.NoError(t, db.Table("gateway_budget_effects").Count(&beforeEffects).Error)
				q := query.BindGatewayRequests(db, m.names)
				before, err := q.WithContext(ctx).Order(q.ID).Find()
				require.NoError(t, err)
				count := 0
				injected := errors.New("gateway ledger storage unavailable")
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
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("gateway_storage_failure", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("gateway_storage_failure", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("gateway_storage_failure", hook))
				}
				err = db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "admit":
						_, e := m.prepareGatewayProxyOn(ctx, tx, 7, input)
						return e
					case "settle":
						return m.finishGatewayProxyOn(ctx, tx, route, 12, true, true)
					default:
						return m.gatewayProxyCurrentOn(ctx, tx, route)
					}
				})
				require.NoError(t, db.Callback().Query().Remove("gateway_storage_failure"))
				require.NoError(t, db.Callback().Create().Remove("gateway_storage_failure"))
				require.NoError(t, db.Callback().Update().Remove("gateway_storage_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				after, e := q.WithContext(ctx).Order(q.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				var effects int64
				require.NoError(t, db.Table("gateway_budget_effects").Count(&effects).Error)
				require.Equal(t, beforeEffects, effects)
				return count
			}
			count := exercise(t, 0)
			for point := 1; point <= count; point++ {
				t.Run(fmt.Sprintf("%s/%s/%d", command, operation, point), func(t *testing.T) { exercise(t, point) })
			}
		}
	}
}

func TestGatewayProxyRealTLSUpstreamAndAtomicSettlement(t *testing.T) {
	for _, status := range []string{"completed", "incomplete", "failed"} {
		for _, streaming := range []bool{false, true} {
			t.Run(status+map[bool]string{false: "json", true: "sse"}[streaming], func(t *testing.T) {
				m, db, ctx, _, publisher := enrollmentHarness(t)
				m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
				var key GatewayKeyView
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					key, err = m.storeGatewayKeyOn(ctx, tx, 7, "upstream-private-credential", true)
					return err
				}))
				calls := 0
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					assert.Equal(t, "/v1/responses", r.URL.Path)
					assert.Equal(t, "Bearer upstream-private-credential", r.Header.Get("Authorization"))
					assert.Empty(t, r.Header.Get("Cookie"))
					assert.Empty(t, r.Header.Get("Origin"))
					var body map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					assert.NoError(t, decoder.Decode(&body))
					assert.Equal(t, json.Number("10"), body["max_output_tokens"])
					assert.Equal(t, false, body["store"])
					payload := `{"status":"` + status + `","usage":{"input_tokens":3,"output_tokens":4}}`
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"type\":\"response."+status+"\",\"response\":"+payload+"}\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, payload)
					}
				}))
				defer upstream.Close()
				in := gatewayWrite()
				in.Providers[0].BaseURL = upstream.URL + "/v1"
				in.Providers[0].KeyRef = &key.ID
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 0, in); return err }))
				// Enabling direct-provider dispatch uses the existing persisted flag;
				// this fixture has no machine inventory and never fabricates one.
				require.NoError(t, db.Table(m.names.GatewayConfigs.String()).Where("tenant_id = ?", 7).Update("enabled", true).Error)
				require.NoError(t, db.Exec("CREATE TABLE proxy_costs(kind TEXT, amount BIGINT)").Error)
				m.gatewayBudgets.ReserveOn = func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID, cost int64, limit *int64) error {
					require.Equal(t, guid.ID(7), tenant)
					require.Equal(t, guid.ID(20), project)
					require.Positive(t, id)
					require.Equal(t, int64(70), cost)
					return tx.Exec("INSERT INTO proxy_costs VALUES ('reserved',?)", cost).Error
				}
				m.gatewaySettlement = func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID, cost int64) error {
					return tx.Exec("INSERT INTO proxy_costs VALUES ('settled',?)", cost).Error
				}
				payload := json.RawMessage(`{"input":"abc","stream":false}`)
				if streaming {
					payload = json.RawMessage(`{"input":"abc","stream":true}`)
				}
				var route gatewayRoute
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					route, err = m.prepareGatewayProxyOn(ctx, tx, 7, GatewayProxyInput{ProjectID: 20, MaxInputTokens: 10, MaxOutputTokens: 10, Request: payload})
					return err
				}))
				response, err := gatewayUpstream(ctx, route, upstream.Client().Transport)
				require.NoError(t, err)
				var output bytes.Buffer
				flushes := 0
				cost, known, completed, err := copyGatewayResponse(&output, response.Body, streaming, route.price, func() error { flushes++; return nil })
				require.NoError(t, response.Body.Close())
				require.NoError(t, err)
				require.True(t, known)
				require.Equal(t, status == "completed", completed)
				require.Equal(t, int64(26), cost)
				require.Positive(t, flushes)
				require.Equal(t, 1, calls)
				publisher.fail = true
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.finishGatewayProxyOn(ctx, tx, route, cost, known, completed) }))
				var settled int64
				require.NoError(t, db.Table("proxy_costs").Where("kind = 'settled'").Count(&settled).Error)
				require.Zero(t, settled)
				publisher.fail = false
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.finishGatewayProxyOn(ctx, tx, route, cost, known, completed) }))
				q := query.BindGatewayRequests(db, m.names)
				row, err := q.WithContext(ctx).Where(q.ID.Eq(route.request.ID)).First()
				require.NoError(t, err)
				if completed {
					require.Equal(t, "COMPLETED", row.State)
				} else {
					require.Equal(t, "FAILED", row.State)
					require.Equal(t, "UPSTREAM_FAILED", row.Reason)
				}
				encoded, err := json.Marshal(row)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "credential")
				require.NotContains(t, string(encoded), "abc")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.finishGatewayProxyOn(ctx, tx, route, cost, known, completed) }), ErrVersionConflict)
			})
		}
	}
}

func TestGatewayProxyUsageUnknownCachePricingAndStreamingBoundaries(t *testing.T) {
	price := GatewayModel{InputMicros: 2000000, OutputMicros: 4000000, CacheReadMicros: 1000000, CacheWriteMicros: 3000000}
	for _, raw := range []string{`{}`, `{"status":"completed","usage":{"input_tokens":-1,"output_tokens":0}}`, `{"status":"completed","usage":{"input_tokens":1}}`, `{"status":"completed","usage":{"input_tokens":1,"output_tokens":0,"input_tokens_details":{"cached_tokens":2}}}`} {
		_, known, _ := gatewayUsageCost([]byte(raw), price)
		require.False(t, known)
	}
	cost, known, _ := gatewayUsageCost([]byte(`{"status":"completed","usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":2}}}`), price)
	require.True(t, known)
	require.Equal(t, int64(27), cost)
	var output bytes.Buffer
	_, known, _, err := copyGatewayResponse(&output, strings.NewReader("data: partial\n\n"), true, price, nil)
	require.NoError(t, err)
	require.False(t, known)
	require.Equal(t, "data: partial\n\n", output.String())
	_, _, _, err = copyGatewayResponse(io.Discard, strings.NewReader(strings.Repeat("x", 901*1024)), false, price, nil)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, _, err = copyGatewayResponse(io.Discard, strings.NewReader("x"), true, price, func() error { return io.ErrClosedPipe })
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestGatewayProxyNeverFollowsCredentialRedirectOrLeaksTransportErrors(t *testing.T) {
	targetCalls := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls++ }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	r, err := gatewayUpstream(t.Context(), gatewayRoute{provider: GatewayProvider{BaseURL: source.URL}, body: []byte(`{}`), key: "private"}, source.Client().Transport)
	require.NoError(t, err)
	require.NoError(t, r.Body.Close())
	require.Equal(t, http.StatusTemporaryRedirect, r.StatusCode)
	require.Zero(t, targetCalls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, err = gatewayUpstream(ctx, gatewayRoute{provider: GatewayProvider{BaseURL: source.URL}, body: []byte(`{}`), key: "private"}, source.Client().Transport)
	if r != nil {
		require.NoError(t, r.Body.Close())
	}
	require.ErrorIs(t, err, ErrUnavailable)
	require.NotContains(t, err.Error(), source.URL)
}

func TestGatewayProxyAdmissionUnavailableInvalidAndAtomicFailures(t *testing.T) {
	m, db, ctx, _, publisher := enrollmentHarness(t)
	input := GatewayProxyInput{ProjectID: 20, MaxInputTokens: 10, MaxOutputTokens: 10, Request: json.RawMessage(`{"input":"hello"}`)}
	prepare := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, err := m.prepareGatewayProxyOn(ctx, tx, 7, input); return err })
	}
	require.ErrorIs(t, prepare(), ErrUnavailable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 0, gatewayWrite()); return err }))
	require.NoError(t, db.Table(m.names.GatewayConfigs.String()).Where("tenant_id = ?", 7).Update("enabled", true).Error)
	m.gatewaySettlement = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error { return nil }
	reservations := 0
	m.gatewayBudgets.ReserveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64, *int64) error {
		reservations++
		return errors.New("denied")
	}
	require.ErrorContains(t, prepare(), "denied")
	require.Equal(t, 1, reservations)
	for _, body := range []string{`{"input":[{"type":"input_image"}]}`, `{"input":"hello","model":"missing"}`, `{"input":"hello","tools":[]}`, `{"input":"hello","previous_response_id":"r"}`, `{"input":"too long for the reservation"}`} {
		input.Request = json.RawMessage(body)
		require.ErrorIs(t, prepare(), ErrInvalidArgument)
	}
	input.Request = json.RawMessage(`{"input":"hello"}`)
	m.gatewayBudgets.ReserveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64, *int64) error { return nil }
	publisher.fail = true
	require.Error(t, prepare())
	var count int64
	require.NoError(t, db.Table(m.names.GatewayRequests.String()).Count(&count).Error)
	require.Zero(t, count)
	s := module.NewServices()
	require.NoError(t, m.provideGatewaySettlement(s))
	require.NoError(t, s.Seal())
	connector, err := module.Resolve[GatewaySettlementConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(nil), ErrUnavailable)
}

func TestGatewayProxyStreamingBoundsEachEventNotWholeConversation(t *testing.T) {
	price := GatewayModel{InputMicros: 1000000, OutputMicros: 1000000}
	prefix := strings.Repeat("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", 20000)
	terminal := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n"
	var output bytes.Buffer
	cost, known, completed, err := copyGatewayResponse(&output, strings.NewReader(prefix+terminal), true, price, nil)
	require.NoError(t, err)
	require.True(t, known)
	require.True(t, completed)
	require.Equal(t, int64(3), cost)
	require.Equal(t, prefix+terminal, output.String())
	_, _, _, err = copyGatewayResponse(io.Discard, strings.NewReader("data: "+strings.Repeat("x", 901*1024)), true, price, nil)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, _, err = copyGatewayResponse(io.Discard, strings.NewReader(strings.Repeat("x", 900*1024+1)+"\n"), true, price, nil)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestGatewayProviderCredentialCannotBeRandomlyRotated(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	var key GatewayKeyView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		key, err = m.storeGatewayKeyOn(ctx, tx, 7, "actual-provider-credential", true)
		return err
	}))
	in := gatewayWrite()
	in.Providers[0].KeyRef = &key.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 0, in); return err }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.RotateGatewayKeyOn(ctx, tx, 7, key.ID, 1, "Rotate imported provider credential")
		return err
	}), ErrInvalidArgument)
	q := query.BindGatewayKeys(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(key.ID)).First()
	require.NoError(t, err)
	require.Nil(t, row.RevokedAt)
	plain, err := m.openGatewayKey(7, row)
	require.NoError(t, err)
	require.Equal(t, "actual-provider-credential", string(plain))
	clear(plain)
	var view GatewayView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; view, err = m.GatewayOn(ctx, tx, 7); return err }))
	require.Equal(t, key.ID, *view.Providers[0].KeyRef)
}

func TestGatewayAdmissionSealedProviderAndPayloadFailuresNeverReserveBudget(t *testing.T) {
	for _, fault := range []string{"malformed", "null", "model_type", "stream_type", "missing_input", "input_type", "input_bound", "output_bound", "body_bound", "key_missing", "key_unseal", "id_error", "id_zero", "authority", "key_source"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := enrollmentHarness(t)
			m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
			var key GatewayKeyView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				key, e = m.storeGatewayKeyOn(ctx, tx, 7, "real-provider-key", true)
				return e
			}))
			in := gatewayWrite()
			in.Providers[0].KeyRef = &key.ID
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 0, in); return e }))
			require.NoError(t, db.Table(m.names.GatewayConfigs.String()).Where("tenant_id=?", 7).Update("enabled", true).Error)
			reserved := 0
			m.gatewaySettlement = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error { return nil }
			m.gatewayBudgets.ReserveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64, *int64) error {
				reserved++
				return nil
			}
			input := GatewayProxyInput{ProjectID: 20, MaxInputTokens: 10, MaxOutputTokens: 10, Request: json.RawMessage(`{"input":"hello"}`)}
			injected := errors.New("admission current source failure")
			switch fault {
			case "malformed":
				input.Request = json.RawMessage(`{`)
			case "null":
				input.Request = json.RawMessage(`null`)
			case "model_type":
				input.Request = json.RawMessage(`{"input":"hello","model":[]}`)
			case "stream_type":
				input.Request = json.RawMessage(`{"input":"hello","stream":"yes"}`)
			case "missing_input":
				input.Request = json.RawMessage(`{}`)
			case "input_type":
				input.Request = json.RawMessage(`{"input":[]}`)
			case "input_bound":
				input.MaxInputTokens = 0
			case "output_bound":
				input.MaxOutputTokens = 1000001
			case "body_bound":
				input.Request = json.RawMessage(strings.Repeat("x", 1024*1024+1))
			case "key_missing":
				require.NoError(t, db.Table(m.names.GatewayKeys.String()).Where("id=?", key.ID).Delete(&struct{}{}).Error)
			case "key_unseal":
				m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
			case "id_error":
				m.ids = failedFleetIDs{err: injected}
			case "id_zero":
				m.ids = failedFleetIDs{}
			case "authority":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "key_source":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("gateway_key_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.GatewayKeys.String() {
						tx.AddError(injected)
					}
				}))
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.prepareGatewayProxyOn(ctx, tx, 7, input); return e }))
			require.NoError(t, db.Callback().Query().Remove("gateway_key_source_failure"))
			require.Zero(t, reserved)
			var rows int64
			require.NoError(t, db.Table(m.names.GatewayRequests.String()).Count(&rows).Error)
			require.Zero(t, rows)
		})
	}
}

func TestGatewayResponseTransportFailurePreservesObservedUsageAndStopsCopy(t *testing.T) {
	payload := `{"status":"completed","usage":{"input_tokens":2,"output_tokens":3}}`
	price := GatewayModel{InputMicros: 1000000, OutputMicros: 1000000}
	reader, writer := io.Pipe()
	injected := errors.New("upstream response interrupted")
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.WriteString(writer, payload); _ = writer.CloseWithError(injected) }()
	var out bytes.Buffer
	cost, known, completed, e := copyGatewayResponse(&out, reader, false, price, nil)
	require.ErrorIs(t, e, injected)
	require.NoError(t, reader.Close())
	<-done
	require.Equal(t, payload, out.String())
	require.EqualValues(t, 5, cost)
	require.True(t, known)
	require.True(t, completed)
	destination, closed := io.Pipe()
	require.NoError(t, destination.Close())
	cost, known, completed, e = copyGatewayResponse(closed, strings.NewReader(payload), false, price, nil)
	require.ErrorIs(t, e, io.ErrClosedPipe)
	require.NoError(t, closed.Close())
	require.EqualValues(t, 5, cost)
	require.True(t, known)
	require.True(t, completed)
}

func TestGatewayCurrentSealedKeyAndSettlementOwnerFailuresPreserveRunningRequest(t *testing.T) {
	for _, fault := range []string{"none", "authority", "config_revision", "key_missing", "key_source", "settlement_missing", "settlement_failure", "no_transaction", "empty_route"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := enrollmentHarness(t)
			m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
			var key GatewayKeyView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				key, e = m.storeGatewayKeyOn(ctx, tx, 7, "actual-current-provider-secret", true)
				return e
			}))
			in := gatewayWrite()
			in.Providers[0].KeyRef = &key.ID
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 0, in); return e }))
			require.NoError(t, db.Table(m.names.GatewayConfigs.String()).Where("tenant_id=?", 7).Update("enabled", true).Error)
			m.gatewayBudgets.ReserveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64, *int64) error { return nil }
			m.gatewaySettlement = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error { return nil }
			var route gatewayRoute
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				route, e = m.prepareGatewayProxyOn(ctx, tx, 7, GatewayProxyInput{ProjectID: 20, MaxInputTokens: 10, MaxOutputTokens: 10, Request: json.RawMessage(`{"input":"hello"}`)})
				return e
			}))
			injected := errors.New("current gateway source unavailable")
			switch fault {
			case "authority":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "config_revision":
				require.NoError(t, db.Table(m.names.GatewayConfigs.String()).Where("tenant_id=?", 7).Update("revision", 2).Error)
			case "key_missing":
				require.NoError(t, db.Table(m.names.GatewayKeys.String()).Where("id=?", key.ID).Delete(nil).Error)
			case "key_source":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("current_key_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.GatewayKeys.String() {
						tx.AddError(injected)
					}
				}))
			case "settlement_missing":
				m.gatewaySettlement = nil
			case "settlement_failure":
				m.gatewaySettlement = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error { return injected }
			}
			var e error
			if fault == "no_transaction" {
				e = m.finishGatewayProxyOn(ctx, db, route, 1, true, true)
			} else {
				e = db.Transaction(func(tx *gorm.DB) error {
					if fault == "empty_route" {
						require.ErrorIs(t, m.gatewayProxyCurrentOn(ctx, tx, gatewayRoute{}), ErrInvalidArgument)
						return m.finishGatewayProxyOn(ctx, tx, gatewayRoute{}, 1, true, true)
					}
					if strings.HasPrefix(fault, "settlement_") {
						return m.finishGatewayProxyOn(ctx, tx, route, 1, true, true)
					}
					return m.gatewayProxyCurrentOn(ctx, tx, route)
				})
			}
			require.NoError(t, db.Callback().Query().Remove("current_key_failure"))
			if fault == "none" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
			q := query.BindGatewayRequests(db, m.names)
			saved, e := q.WithContext(ctx).Where(q.ID.Eq(route.request.ID)).First()
			require.NoError(t, e)
			require.Equal(t, "RUNNING", saved.State)
			require.Nil(t, saved.FinishedAt)
		})
	}
}

func TestGatewayUnusableUpstreamAndCorruptPriceNeverProduceBillableUsage(t *testing.T) {
	for _, price := range []GatewayModel{{InputMicros: -1}, {InputMicros: 9223372036854775807, OutputMicros: 9223372036854775807}} {
		cost, known, completed := gatewayUsageCost([]byte(`{"status":"completed","usage":{"input_tokens":9223372036854775807,"output_tokens":1}}`), price)
		require.Zero(t, cost)
		require.False(t, known)
		require.False(t, completed)
	}
	response, e := gatewayUpstream(t.Context(), gatewayRoute{provider: GatewayProvider{BaseURL: "https://%invalid"}}, http.DefaultTransport)
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	require.ErrorIs(t, e, ErrUnavailable)
	require.Nil(t, response)
}
