package fleet

import (
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
)

func TestMachineRevocationEveryStorageFailurePreservesSessionAndAccount(t *testing.T) {
	for _, operation := range []string{"query", "update"} {
		exercise := func(t *testing.T, failAt int) int {
			f := agentRebindHarness(t)
			q := query.BindMachines(f.db, f.m.names)
			before, err := q.WithContext(t.Context()).Where(q.ID.Eq(guid.ID(1))).First()
			require.NoError(t, err)
			s := query.BindSessions(f.db, f.m.names)
			sessions, err := s.WithContext(t.Context()).Order(s.ID).Find()
			require.NoError(t, err)
			f.m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
			f.m.revokeAccount = func(_ context.Context, tx *gorm.DB, _, _ guid.ID) error {
				return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('account_disabled')").Error
			}
			injected := errors.New("machine revocation storage unavailable")
			count := 0
			hook := func(tx *gorm.DB) {
				if !strings.HasPrefix(tx.Statement.Table, "dir_fleet_") {
					return
				}
				count++
				if count == failAt {
					tx.AddError(injected)
				}
			}
			if operation == "query" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("revocation_storage_failure", hook))
			} else {
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("revocation_storage_failure", hook))
			}
			err = f.db.Transaction(func(tx *gorm.DB) error {
				_, e := f.m.RevokeMachineOn(f.human, tx, 7, 1, before.Version, before.Name, "Device permanently retired", "browser", "fresh")
				return e
			})
			require.NoError(t, f.db.Callback().Query().Remove("revocation_storage_failure"))
			require.NoError(t, f.db.Callback().Update().Remove("revocation_storage_failure"))
			if failAt == 0 {
				require.NoError(t, err)
				return count
			}
			require.ErrorIs(t, err, injected)
			after, e := q.WithContext(t.Context()).Where(q.ID.Eq(guid.ID(1))).First()
			require.NoError(t, e)
			require.Equal(t, before, after)
			afterSessions, e := s.WithContext(t.Context()).Order(s.ID).Find()
			require.NoError(t, e)
			require.Equal(t, sessions, afterSessions)
			var effects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Where("kind=?", "account_disabled").Count(&effects).Error)
			require.Zero(t, effects)
			return count
		}
		count := exercise(t, 0)
		require.Positive(t, count)
		for point := 1; point <= count; point++ {
			t.Run(fmt.Sprintf("%s/%d", operation, point), func(t *testing.T) { exercise(t, point) })
		}
	}
}

func TestRealtimeMachineTopicsUseCurrentTenantAndMachineReadVisibility(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	for _, input := range []struct {
		tenant, principal, id guid.ID
		collection, allowed   bool
	}{{7, 101, 1, false, true}, {7, 101, 0, true, true}, {7, 101, 999, false, false}, {8, 101, 1, false, false}, {7, 102, 1, false, false}, {7, 101, 0, false, false}} {
		allowed, err := m.authorizeRealtimeMachine(ctx, input.tenant, input.principal, input.id, input.collection)
		require.NoError(t, err)
		require.Equal(t, input.allowed, allowed)
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	allowed, err := m.authorizeRealtimeMachine(ctx, 7, 101, 1, false)
	require.NoError(t, err)
	require.False(t, allowed)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrUnavailable
	}
	_, err = m.authorizeRealtimeMachine(ctx, 7, 101, 1, false)
	require.ErrorIs(t, err, ErrUnavailable)
	m.policy = nil
	_, err = m.authorizeRealtimeMachine(ctx, 7, 101, 1, false)
	require.ErrorIs(t, err, ErrUnavailable)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Migrator().DropTable(m.names.Machines.String()))
	_, err = m.authorizeRealtimeMachine(ctx, 7, 101, 1, false)
	require.Error(t, err)
}

func TestMachineRevocationHTTPBrowserProofAndIdempotentRetry(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
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
	spent := 0
	m.revokeProof = func(_ context.Context, _ *gorm.DB, _ guid.ID, cookie, proof, purpose, target string) error {
		if cookie != "browser=session" || proof != "fresh" || purpose != "machine_revoke" || target != "1" {
			return authn.ErrInvalidCredentials
		}
		spent++
		return nil
	}
	m.revokeAccount = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	m.revokeBrowser.ValidateCSRF = func(method, origin, cookie string) error {
		if method != http.MethodPost || origin != "https://app.example.test" || cookie != "browser=session" {
			return ErrMachineForbidden
		}
		return nil
	}
	router := fleetBusinessRouter(t, m)
	q := query.BindMachines(db, m.names)
	row, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	body := `{"confirm_name":"` + row.Name + `","reason":"device lost","reauth_proof":"fresh"}`
	headers := map[string]string{"Cookie": "browser=session", "Origin": "https://evil.example", "If-Match": "1", "Idempotency-Key": "revoke-once"}
	out := fleetHTTPRequest(router, http.MethodPost, "/machines/1/revocation", body, headers)
	require.Equal(t, http.StatusForbidden, out.Code, out.Body.String())
	require.Zero(t, spent)
	headers["Origin"] = "https://app.example.test"
	out = fleetHTTPRequest(router, http.MethodPost, "/machines/1/revocation", body, headers)
	require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
	require.Equal(t, 1, spent)
	out = fleetHTTPRequest(router, http.MethodPost, "/machines/1/revocation", body, headers)
	require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
	require.Equal(t, 1, spent, "cached retry must not spend proof twice")
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	out = fleetHTTPRequest(router, http.MethodPost, "/machines/1/revocation", body, headers)
	require.Equal(t, http.StatusForbidden, out.Code, out.Body.String())
}

func TestMachineRevocationProofAndFencesAreAtomic(t *testing.T) {
	for _, fault := range []string{"", "proof", "account", "name", "version", "event", "authority", "cookie"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, p := machineHarness(t)
			m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, true, nil
			}
			m.policy = func(_ context.Context, _ *gorm.DB, _ guid.ID, code string, _ guid.ID) (authz.DataConstraint, error) {
				require.Equal(t, "machine_revoke", code)
				return authz.DataConstraint{AllowAll: fault != "authority"}, nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "runner"); return err }))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReserveSlotOn(ctx, tx, 7, 1, 91); return err }))
			m.revokeProof = func(_ context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
				require.Equal(t, guid.ID(101), actor)
				require.Equal(t, "cookie", cookie)
				require.Equal(t, "proof", proof)
				require.Equal(t, "machine_revoke", purpose)
				require.Equal(t, "1", target)
				if fault == "proof" {
					return authn.ErrInvalidCredentials
				}
				return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('proof')").Error
			}
			m.revokeAccount = func(_ context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
				require.Equal(t, guid.ID(7), tenant)
				require.Equal(t, guid.ID(101), principal)
				if fault == "account" {
					return ErrUnavailable
				}
				return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('account')").Error
			}
			name, version, cookie := "Machine one", 2, "cookie"
			if fault == "name" {
				name = "wrong"
			}
			if fault == "version" {
				version = 1
			}
			if fault == "cookie" {
				cookie = ""
			}
			p.fail = fault == "event"
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.RevokeMachineOn(ctx, tx, 7, 1, version, name, "lost device", cookie, "proof")
				return err
			})
			q := query.BindMachines(db, m.names)
			row, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			var spent int64
			require.NoError(t, db.Table("test_fleet_effects").Where("kind IN ?", []string{"proof", "account"}).Count(&spent).Error)
			if fault != "" {
				require.Error(t, err)
				require.Equal(t, "ONLINE", row.State)
				require.Zero(t, spent)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "REVOKED", row.State)
			require.Nil(t, row.CurrentSessionID)
			require.EqualValues(t, 2, spent)
			sessions := query.BindSessions(db, m.names)
			session, e := sessions.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, "CLOSED", session.State)
			slots := query.BindSlots(db, m.names)
			slot, e := slots.WithContext(ctx).First()
			require.NoError(t, e)
			require.Nil(t, slot.ActiveSlot)
			require.NotNil(t, slot.ReleasedAt)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.HeartbeatOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch) }), ErrMachineRevoked)
		})
	}
}

func TestRotationNotificationRequesterIsTenantAndMachineScoped(t *testing.T) {
	f := rotationHarness(t)
	rotation, err := f.m.RequestCredentialRotation(f.ctx, 7, 1)
	require.NoError(t, err)
	id, err := f.m.rotationNotificationRequester(f.ctx, f.db, 7, 1, rotation.ID)
	require.NoError(t, err)
	require.NotZero(t, id)
	id, err = f.m.rotationNotificationRequester(f.ctx, f.db, 8, 1, rotation.ID)
	require.NoError(t, err)
	require.Zero(t, id)
	id, err = f.m.rotationNotificationRequester(f.ctx, f.db, 7, 2, rotation.ID)
	require.NoError(t, err)
	require.Zero(t, id)
	_, err = f.m.rotationNotificationRequester(f.ctx, nil, 7, 1, rotation.ID)
	require.Error(t, err)
}

func TestRotationNotificationFactsCurrentStateAndTenantIsolation(t *testing.T) {
	f := rotationHarness(t)
	rotation, err := f.requestRotation()
	require.NoError(t, err)
	fact, exists, err := f.m.rotationNotificationFacts(f.ctx, f.db, 7, 1, rotation.ID)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, "PENDING_DELIVERY", fact.State)
	require.Equal(t, rotation.RetireOldAt, fact.RetireOldAt)
	require.NotZero(t, fact.RequestedBy)
	for _, ids := range [][3]guid.ID{{8, 1, rotation.ID}, {7, 2, rotation.ID}, {7, 1, rotation.ID + 1}} {
		_, exists, err = f.m.rotationNotificationFacts(f.ctx, f.db, ids[0], ids[1], ids[2])
		require.NoError(t, err)
		require.False(t, exists)
	}
	q := query.BindMachines(f.db, f.m.names)
	_, err = q.WithContext(f.ctx).Where(q.ID.Eq(guid.ID(1))).Updates(map[string]any{"state": "REVOKED", "name_key": nil})
	require.NoError(t, err)
	_, exists, err = f.m.rotationNotificationFacts(f.ctx, f.db, 7, 1, rotation.ID)
	require.NoError(t, err)
	require.False(t, exists)
	_, _, err = f.m.rotationNotificationFacts(f.ctx, nil, 7, 1, rotation.ID)
	require.Error(t, err)
}

func TestMachineRevocationCurrentProofPrincipalAndVersionFailuresPreserveSessions(t *testing.T) {
	for _, fault := range []string{"proof_missing", "account_missing", "cookie", "proof", "proof_source", "proof_invalid", "principal_lock", "version", "confirm_name", "reason", "authority"} {
		t.Run(fault, func(t *testing.T) {
			f := agentRebindHarness(t)
			m := f.m
			m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
			m.revokeAccount = func(_ context.Context, tx *gorm.DB, _, _ guid.ID) error {
				return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES ('revoked')").Error
			}
			q := query.BindMachines(f.db, m.names)
			before, e := q.WithContext(f.human).Where(q.ID.Eq(guid.ID(1))).First()
			require.NoError(t, e)
			sq := query.BindSessions(f.db, m.names)
			sessions, e := sq.WithContext(f.human).Order(sq.ID).Find()
			require.NoError(t, e)
			var baseline int64
			require.NoError(t, f.db.Table("test_fleet_effects").Count(&baseline).Error)
			cookie, proof, name, reason := "browser", "fresh", before.Name, "Revoke current machine credential"
			version := before.Version
			injected := errors.New("revocation current source failure")
			switch fault {
			case "proof_missing":
				m.revokeProof = nil
			case "account_missing":
				m.revokeAccount = nil
			case "cookie":
				cookie = ""
			case "proof":
				proof = ""
			case "proof_source":
				m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return injected }
			case "proof_invalid":
				m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error {
					return authn.ErrInvalidCredentials
				}
			case "principal_lock":
				m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error {
					if id == before.PrincipalID {
						return injected
					}
					return nil
				}
			case "version":
				version++
			case "confirm_name":
				name = "unconfirmed other machine"
			case "reason":
				reason = ""
			case "authority":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, e := m.RevokeMachineOn(f.human, tx, 7, 1, version, name, reason, cookie, proof)
				return e
			}))
			after, e := q.WithContext(f.human).Where(q.ID.Eq(guid.ID(1))).First()
			require.NoError(t, e)
			require.Equal(t, before, after)
			current, e := sq.WithContext(f.human).Order(sq.ID).Find()
			require.NoError(t, e)
			require.Equal(t, sessions, current)
			var effects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Count(&effects).Error)
			require.Equal(t, baseline, effects)
		})
	}
}
