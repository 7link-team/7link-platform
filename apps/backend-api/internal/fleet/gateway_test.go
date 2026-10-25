package fleet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/keyringx"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func gatewayWrite() GatewayWrite {
	return GatewayWrite{DefaultModel: "gpt-test", GatewayRecords: GatewayRecords{Providers: []GatewayProvider{{ID: "provider", BaseURL: "https://example.invalid/v1"}}, Models: []GatewayModel{{ID: "gpt-test", Provider: "provider", Runtime: "CODEX", InputMicros: 2000000, OutputMicros: 5000000}}, Budgets: []GatewayBudget{{ProjectID: 20, LimitMicros: 123456}}}}
}

func TestGatewayRotationRevokesOldKeyAndRollsBackSourceFacts(t *testing.T) {
	m, db, ctx, _, publisher := enrollmentHarness(t)
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	var old, replacement GatewayKeyView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		old, err = m.CreateGatewayKeyOn(ctx, tx, 7)
		if err != nil {
			return err
		}
		in := gatewayWrite()
		in.KeyRef, in.Providers[0].KeyRef = &old.ID, &old.ID
		_, err = m.SaveGatewayOn(ctx, tx, 7, 0, in)
		return err
	}))
	rotate := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			var err error
			replacement, err = m.RotateGatewayKeyOn(ctx, tx, 7, old.ID, 1, "Rotate the configured gateway credential")
			return err
		})
	}
	publisher.fail = true
	require.Error(t, rotate())
	q := query.BindGatewayKeys(db, m.names)
	saved, err := q.WithContext(ctx).Where(q.ID.Eq(old.ID)).First()
	require.NoError(t, err)
	require.Nil(t, saved.RevokedAt)
	require.NotEmpty(t, saved.Sealed)
	n, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	publisher.fail = false
	require.NoError(t, rotate())
	saved, err = q.WithContext(ctx).Where(q.ID.Eq(old.ID)).First()
	require.NoError(t, err)
	require.NotNil(t, saved.RevokedAt)
	require.Empty(t, saved.Sealed, "revoked secret material is no longer usable")
	require.Equal(t, replacement.ID, *saved.ReplacementID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		view, err := m.GatewayOn(ctx, tx, 7)
		require.NoError(t, err)
		require.Equal(t, replacement.ID, *view.KeyRef)
		require.Equal(t, replacement.ID, *view.Providers[0].KeyRef)
		view.KeyRef = &old.ID
		require.ErrorIs(t, m.validateGatewayOn(ctx, tx, 7, view.GatewayWrite), ErrInvalidArgument)
		valid, err := m.gatewayRotationFactOn(ctx, tx, 7, replacement.ID)
		require.NoError(t, err)
		require.True(t, valid)
		valid, err = m.gatewayRotationFactOn(ctx, tx, 8, replacement.ID)
		require.NoError(t, err)
		require.False(t, valid)
		return nil
	}))
	require.ErrorIs(t, rotate(), ErrVersionConflict)
}

func TestGatewayCommandFailuresRollBackAllPersistedFacts(t *testing.T) {
	for _, command := range []string{"config_create", "config_update", "key", "request"} {
		faults := []string{"ids_missing", "ids_error", "ids_zero", "audit_missing", "events_missing", "audit", "outbox", "insert"}
		if command != "key" {
			faults = append(faults, "config_query")
		}
		if command == "config_create" || command == "config_update" {
			faults = append(faults, "price_insert", "price_ids_error", "price_ids_zero")
		}
		if command == "request" {
			faults = append(faults, "ledger_query", "prune")
		}
		for _, fault := range faults {
			t.Run(command+"/"+fault, func(t *testing.T) {
				m, db, ctx, _, publisher := enrollmentHarness(t)
				m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
				if command == "config_update" || command == "request" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 0, gatewayWrite()); return err }))
				}
				if command == "request" {
					now := time.Now().UTC()
					for i := 1; i <= 100; i++ {
						require.NoError(t, query.BindGatewayRequests(db, m.names).WithContext(ctx).Create(&model.GatewayRequest{ID: guid.ID(i), TenantID: 7, Revision: 1, Model: "gpt-test", State: "FAILED", Reason: "RUNNER_GATEWAY_UNAVAILABLE", CreatedAt: now, FinishedAt: &now}))
					}
				}
				tables := []string{m.names.GatewayConfigs.String(), m.names.GatewayKeys.String(), m.names.GatewayRequests.String(), m.names.Prices.String(), "test_fleet_effects"}
				snapshot := func() []string {
					var out []string
					for _, table := range tables {
						var rows []map[string]any
						require.NoError(t, db.Table(table).Find(&rows).Error)
						b, err := json.Marshal(rows)
						require.NoError(t, err)
						out = append(out, string(b))
					}
					return out
				}
				before := snapshot()
				failure := errors.New("gateway injected failure")
				queryTable := ""
				switch fault {
				case "ids_missing":
					m.ids = nil
				case "ids_error":
					m.ids = failedFleetIDs{err: failure}
				case "ids_zero":
					m.ids = failedFleetIDs{}
				case "price_ids_error", "price_ids_zero":
					calls := 0
					m.ids = gatewayTestIDs(func() (int64, error) {
						calls++
						if command == "config_create" && calls == 1 {
							return 999999, nil
						}
						if fault == "price_ids_error" {
							return 0, failure
						}
						return 0, nil
					})
				case "audit_missing":
					m.audit = nil
				case "events_missing":
					m.events = nil
				case "audit":
					m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
				case "outbox":
					publisher.fail = true
				case "config_query":
					queryTable = m.names.GatewayConfigs.String()
				case "ledger_query":
					queryTable = m.names.GatewayRequests.String()
				case "insert", "price_insert", "prune":
					table, operation := m.names.GatewayConfigs.String(), "INSERT"
					if command == "config_update" {
						operation = "UPDATE"
					}
					if command == "key" {
						table = m.names.GatewayKeys.String()
					}
					if command == "request" {
						table = m.names.GatewayRequests.String()
					}
					if fault == "price_insert" {
						table, operation = m.names.Prices.String(), "INSERT"
					}
					if fault == "prune" {
						operation = "DELETE"
					}
					require.NoError(t, db.Exec(fmt.Sprintf("CREATE TRIGGER reject_gateway_write BEFORE %s ON %s BEGIN SELECT RAISE(ABORT,'gateway storage unavailable'); END", operation, table)).Error)
				}
				if queryTable != "" {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reject_gateway_query", func(tx *gorm.DB) {
						if tx.Statement.Table == queryTable {
							tx.AddError(failure)
						}
					}))
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "key":
						_, err := m.CreateGatewayKeyOn(ctx, tx, 7)
						return err
					case "request":
						_, err := m.QueueGatewayRequestOn(ctx, tx, 7, "gpt-test")
						return err
					default:
						version := 0
						if command == "config_update" {
							version = 1
						}
						in := gatewayWrite()
						in.Models[0].InputMicros++
						_, err := m.SaveGatewayOn(ctx, tx, 7, version, in)
						return err
					}
				})
				require.Error(t, err)
				if queryTable != "" {
					require.NoError(t, db.Callback().Query().Remove("reject_gateway_query"))
				}
				require.Equal(t, before, snapshot(), "failed command must preserve config, keys, prices, retained ledger and audit/outbox together")
			})
		}
	}
}

type gatewayTestIDs func() (int64, error)

func (f gatewayTestIDs) Next() (int64, error) { return f() }

func TestGatewayReadCorruptionAndCrossTenantKeyReferencesFailClosed(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	var key GatewayKeyView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; key, err = m.CreateGatewayKeyOn(ctx, tx, 7); return err }))
	foreign := authn.WithClaims(ctx, &authn.Claims{TenantID: 8, PrincipalID: 101})
	for _, root := range []bool{false, true} {
		in := gatewayWrite()
		if root {
			in.KeyRef = &key.ID
		} else {
			in.Providers[0].KeyRef = &key.ID
		}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(foreign, tx, 8, 0, in); return err }), ErrInvalidArgument)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 0, gatewayWrite()); return err }))
	require.NoError(t, db.Table(m.names.GatewayConfigs.String()).Where("tenant_id = ?", 7).Update("records_json", "invalid-json").Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.GatewayOn(ctx, tx, 7); return err }), ErrUnavailable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.QueueGatewayRequestOn(ctx, tx, 7, "gpt-test"); return err }), ErrUnavailable)
	var count int64
	require.NoError(t, db.Table(m.names.GatewayRequests.String()).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Exec("DROP TABLE "+m.names.GatewayConfigs.String()).Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.GatewayOn(ctx, tx, 7); return err }))
}

func TestGatewayUnavailableConfigurationCannotCreateSecretsOrRequests(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.QueueGatewayRequestOn(ctx, tx, 7, "gpt-test"); return err }), ErrVersionConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 1, gatewayWrite()); return err }), ErrVersionConflict)
	for _, command := range []string{"key", "config", "request"} {
		switch command {
		case "key":
			_, err := m.CreateGatewayKeyOn(ctx, db, 7)
			require.Error(t, err)
		case "config":
			_, err := m.SaveGatewayOn(ctx, db, 7, 0, gatewayWrite())
			require.Error(t, err)
		case "request":
			_, err := m.QueueGatewayRequestOn(ctx, db, 7, "gpt-test")
			require.Error(t, err)
		}
	}
	for _, invalidKey := range []string{"invalid-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		m.config.GatewayEncryptionKey = invalidKey
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.CreateGatewayKeyOn(ctx, tx, 7); return err }), ErrUnavailable)
	}
	m.config.GatewayEncryptionKey = ""
	m.config.GatewayEncryptionKeyFile = t.TempDir() + "/missing-key"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.CreateGatewayKeyOn(ctx, tx, 7); return err }), ErrUnavailable)
	for _, table := range []string{m.names.GatewayConfigs.String(), m.names.GatewayKeys.String(), m.names.GatewayRequests.String()} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Zero(t, count, "unavailable configuration must not persist facts")
	}
	m.ids = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.GatewayOn(ctx, tx, 7); return err }), ErrUnavailable)
}
func TestGatewayPersistsVersionedRecordsPricesBudgetsAndUnavailableLedger(t *testing.T) {
	m, db, ctx, _, publisher := enrollmentHarness(t)
	var first GatewayView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.GatewayOn(ctx, tx, 7)
		require.NoError(t, e)
		require.Zero(t, out.Version)
		require.False(t, out.Available)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = m.SaveGatewayOn(ctx, tx, 7, 0, gatewayWrite())
		return e
	}))
	require.Equal(t, 1, first.Version)
	require.Equal(t, int64(1), first.Revision)
	require.Equal(t, gatewayWrite().Budgets, first.Budgets)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.GatewayOn(ctx, tx, 7)
		require.NoError(t, e)
		require.Equal(t, first, out)
		price, e := m.ModelPriceOn(ctx, tx, 7, "CODEX", "gpt-test")
		require.NoError(t, e)
		require.Equal(t, int64(2000000), price.InputMicros)
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 0, gatewayWrite()); return e }), ErrVersionConflict)
	in := gatewayWrite()
	in.Models[0].OutputMicros = 6000000
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.SaveGatewayOn(ctx, tx, 7, 1, in)
		require.NoError(t, e)
		require.Equal(t, 2, out.Version)
		return nil
	}))
	publisher.fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 2, gatewayWrite()); return e }))
	publisher.fail = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.GatewayOn(ctx, tx, 7)
		require.NoError(t, e)
		require.Equal(t, 2, out.Version)
		return nil
	}))
	for i := 0; i < 102; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			out, e := m.QueueGatewayRequestOn(ctx, tx, 7, "gpt-test")
			require.NoError(t, e)
			require.Equal(t, "FAILED", out.State)
			require.Equal(t, "RUNNER_GATEWAY_UNAVAILABLE", out.Reason)
			require.NotNil(t, out.FinishedAt)
			require.Nil(t, out.StartedAt)
			require.Equal(t, "gwr_"+out.ID.String(), out.RequestID)
			return nil
		}))
	}
	q := query.BindGatewayRequests(db, m.names)
	count, e := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(7))).Count()
	require.NoError(t, e)
	require.Equal(t, int64(100), count)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.QueueGatewayRequestOn(ctx, tx, 7, "unknown"); return e }), ErrInvalidArgument)
	foreign := authn.WithClaims(ctx, &authn.Claims{TenantID: 8, PrincipalID: 101})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.GatewayOn(foreign, tx, 8)
		require.NoError(t, e)
		require.Zero(t, out.Version)
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.GatewayOn(ctx, tx, 8); return e }), ErrMachineForbidden)
	_, e = m.GatewayOn(ctx, db, 7)
	require.Error(t, e, "caller transaction mandatory")
}
func TestGatewaySealedKeysOpaqueReferencesTenantBindingAndRollback(t *testing.T) {
	m, db, ctx, _, publisher := enrollmentHarness(t)
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	var out GatewayKeyView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.CreateGatewayKeyOn(ctx, tx, 7); return e }))
	require.Positive(t, out.ID)
	require.Len(t, out.Last4, 4)
	q := query.BindGatewayKeys(db, m.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(out.ID)).First()
	require.NoError(t, e)
	require.Contains(t, row.Sealed, "v2.")
	ring, e := keyringx.New("fleet:gateway:", m.config.GatewayEncryptionKey)
	require.NoError(t, e)
	plain, e := ring.Open("credential", []byte("7:"+out.ID.String()), row.Sealed)
	require.NoError(t, e)
	require.Len(t, plain, 43)
	_, e = ring.Open("credential", []byte("8:"+out.ID.String()), row.Sealed)
	require.Error(t, e)
	b, e := json.Marshal(out)
	require.NoError(t, e)
	require.NotContains(t, string(b), string(plain))
	require.NotContains(t, string(b), row.Sealed)
	require.NotContains(t, string(b), row.KeyHash)
	in := gatewayWrite()
	in.Providers[0].KeyRef = &out.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 0, in); return e }))
	publisher.fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.CreateGatewayKeyOn(ctx, tx, 7); return e }))
	publisher.fail = false
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Equal(t, int64(1), n)
	m.config.GatewayEncryptionKey = ""
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.CreateGatewayKeyOn(ctx, tx, 7); return e }), ErrUnavailable)
	in.Providers[0].KeyRef = new(guid.ID(999))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 1, in); return e }), ErrInvalidArgument)
}
func TestGatewayValidationAllRecordsAndCurrentAuthority(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	for _, change := range []func(*GatewayWrite){
		func(v *GatewayWrite) { v.Providers[0].BaseURL = "https://user:secret@example.invalid" },
		func(v *GatewayWrite) { v.Providers[0].BaseURL = "https://example.invalid?token=secret" },
		func(v *GatewayWrite) { v.Providers[0].ID = "bad id" },
		func(v *GatewayWrite) { v.Providers = append(v.Providers, v.Providers[0]) },
		func(v *GatewayWrite) { v.Models[0].Provider = "missing" },
		func(v *GatewayWrite) { v.Models[0].Runtime = "OTHER" },
		func(v *GatewayWrite) { v.Models[0].InputMicros = -1 },
		func(v *GatewayWrite) { v.Models[0].OutputMicros = 9007199254740992 },
		func(v *GatewayWrite) { v.Models = append(v.Models, v.Models[0]) },
		func(v *GatewayWrite) { v.Budgets[0].LimitMicros = -1 },
		func(v *GatewayWrite) { v.Budgets = append(v.Budgets, v.Budgets[0]) },
		func(v *GatewayWrite) { v.DefaultModel = "missing" },
		func(v *GatewayWrite) { v.Enabled = true; v.DefaultModel = "" },
		func(v *GatewayWrite) { v.Enabled = true; v.MachineID = new(guid.ID(999)) },
		func(v *GatewayWrite) { v.MachineID = new(guid.ID(0)) },
	} {
		in := gatewayWrite()
		change(&in)
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 0, in); return e }))
	}
	failure := errors.New("authority failed")
	m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 0, gatewayWrite()); return e }), failure)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.GatewayOn(ctx, tx, 7); return e }), failure)
}

func TestGatewayConfigurationAndRotationEveryStorageFailurePreservesSealedKeyAndVersion(t *testing.T) {
	for _, command := range []string{"save", "rotate"} {
		for _, kind := range []string{"query", "create", "update"} {
			total := 0
			run := func(t *testing.T, at int) {
				m, db, ctx, _, _ := enrollmentHarness(t)
				m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
				var key GatewayKeyView
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; key, e = m.CreateGatewayKeyOn(ctx, tx, 7); return e }))
				in := gatewayWrite()
				in.KeyRef = &key.ID
				in.Providers[0].KeyRef = &key.ID
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.SaveGatewayOn(ctx, tx, 7, 0, in); return e }))
				keys := query.BindGatewayKeys(db, m.names)
				before, e := keys.WithContext(ctx).Order(keys.ID).Find()
				require.NoError(t, e)
				configs := query.BindGatewayConfigs(db, m.names)
				config, e := configs.WithContext(ctx).First()
				require.NoError(t, e)
				count := 0
				injected := errors.New("gateway key storage unavailable")
				hook := func(tx *gorm.DB) {
					if tx.Statement.Table != m.names.GatewayKeys.String() && tx.Statement.Table != m.names.GatewayConfigs.String() {
						return
					}
					count++
					if count == at {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("gateway_configuration_failure", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("gateway_configuration_failure", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("gateway_configuration_failure", hook))
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					if command == "rotate" {
						_, e := m.RotateGatewayKeyOn(ctx, tx, 7, key.ID, 1, "Replace current gateway credential")
						return e
					}
					in.Budgets[0].LimitMicros++
					_, e := m.SaveGatewayOn(ctx, tx, 7, 1, in)
					return e
				})
				require.NoError(t, db.Callback().Query().Remove("gateway_configuration_failure"))
				require.NoError(t, db.Callback().Create().Remove("gateway_configuration_failure"))
				require.NoError(t, db.Callback().Update().Remove("gateway_configuration_failure"))
				if at == 0 {
					require.NoError(t, err)
					total = count
					return
				}
				if kind == "query" && (command == "save" && (at == 2 || at == 3) || command == "rotate" && (at == 4 || at == 5)) {
					require.ErrorIs(t, err, ErrInvalidArgument, "configuration validation hides unavailable key references")
				} else {
					require.ErrorIs(t, err, injected)
				}
				after, e := keys.WithContext(ctx).Order(keys.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				current, e := configs.WithContext(ctx).First()
				require.NoError(t, e)
				require.Equal(t, config, current)
			}
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { run(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/%d", command, kind, at), func(t *testing.T) { run(t, at) })
			}
		}
	}
}
