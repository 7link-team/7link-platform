package core

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestEventsAdministrationRejectsIncompleteSecurityAndFailedBinders(t *testing.T) {
	for _, fault := range []string{"unsealed", "bind_nil", "menu", "bind", "audit_bind", "proof", "store", "appender"} {
		t.Run(fault, func(t *testing.T) {
			h := newRepositoryHarness(t, "sl_")
			m := NewModule(h.runtime, enabledModuleConfig())
			s := module.NewServices()
			failure := errors.New("event admin dependency failed")
			var record events.AdminAudit
			connector := events.AdminConnector{
				Bind: func(_ *authz.Registrar, check func(context.Context, *gorm.DB, string, guid.ID) (bool, error)) error {
					ok, err := check(t.Context(), h.db, "system_event_view", 11)
					require.ErrorIs(t, err, ErrUnavailable)
					require.False(t, ok)
					if fault == "bind" {
						return failure
					}
					return nil
				},
				BindAudit: func(a events.AdminAudit) error {
					record = a
					if fault == "audit_bind" {
						return failure
					}
					return nil
				},
				BindSecurity: func(authn.ReauthenticationConsumer, idempotency.Store) error { return nil },
			}
			if fault == "bind_nil" {
				connector.Bind = nil
			}
			if fault == "menu" {
				require.NoError(t, m.systemMenus.Contribute(SystemMenuPage{Branch: SystemMenuBasicManagement, Menu: Menu{Label: "Existing event page", Route: "/system/domain-events", PermissionCode: "system_event_view", Status: MenuActive}}))
			}
			require.NoError(t, module.Provide(s, connector))
			if fault != "proof" {
				require.NoError(t, module.Provide[authn.ReauthenticationConsumer](s, func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }))
			}
			if fault != "store" {
				require.NoError(t, module.Provide[idempotency.Store](s, idempotency.NewModule(h.runtime)))
			}
			if fault != "unsealed" {
				require.NoError(t, s.Seal())
			}
			err := m.bindEventsAdministration(s)
			switch fault {
			case "unsealed":
				require.ErrorIs(t, err, module.ErrServicesProviding)
			case "bind_nil":
				require.ErrorIs(t, err, ErrUnavailable)
			case "bind", "audit_bind":
				require.ErrorIs(t, err, failure)
			case "proof", "store":
				require.ErrorIs(t, err, module.ErrServiceMissing)
			case "menu":
				require.Error(t, err)
			case "appender":
				require.NoError(t, err)
				require.NotNil(t, record)
				require.ErrorIs(t, record(t.Context(), h.db, "event.replay", 1, "event", 42, nil), ErrUnavailable)
			}
		})
	}
}

func TestJobsAdministrationAuditPreservesDetailsAndFailsUnavailableDependencies(t *testing.T) {
	for _, fault := range []string{"", "bind", "audit_missing", "audit_bind", "appender"} {
		t.Run(fault, func(t *testing.T) {
			h := newRepositoryHarness(t, "sl_")
			m := NewModule(h.runtime, enabledModuleConfig())
			s := module.NewServices()
			failure := errors.New("jobs administration dependency failed")
			var record jobs.AdminAudit
			var event audit.DomainEvent
			m.appender = func(_ context.Context, _ *gorm.DB, in audit.DomainEvent) error { event = in; return nil }
			if fault == "appender" {
				m.appender = nil
			}
			connector := jobs.AdminConnector{Bind: func(_ *authz.Registrar, check func(context.Context, *gorm.DB, string, guid.ID) (bool, error)) error {
				allowed, err := check(t.Context(), h.db, "system_job_view", 11)
				require.ErrorIs(t, err, ErrUnavailable)
				require.False(t, allowed)
				if fault == "bind" {
					return failure
				}
				return nil
			}, BindAudit: func(a jobs.AdminAudit) error {
				record = a
				if fault == "audit_bind" {
					return failure
				}
				return nil
			}}
			if fault == "audit_missing" {
				connector.BindAudit = nil
			}
			require.NoError(t, module.Provide(s, connector))
			require.NoError(t, s.Seal())
			err := m.bindJobsAdministration(s)
			switch fault {
			case "bind", "audit_bind":
				require.ErrorIs(t, err, failure)
				return
			case "audit_missing":
				require.ErrorIs(t, err, ErrUnavailable)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, record)
			err = record(t.Context(), h.db, "jobs.cancel", 1, "job", 42, map[string]any{"reason": "requested"})
			if fault == "appender" {
				require.ErrorIs(t, err, ErrUnavailable)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "requested", event.Detail["reason"])
			require.EqualValues(t, 1, event.TenantID)
		})
	}
}

func TestEventsAdministrationBindsCurrentPermissionAuditAndReplaySecurity(t *testing.T) {
	h := newRepositoryHarness(t, "sl_")
	seedSystemAccessFixture(t, h.db, "sl_", 11)
	m := NewModule(h.runtime, enabledModuleConfig())
	require.NoError(t, m.registrar.Registry().Contribute([]authz.Action{{Resource: "system_event", Verb: "manage", Scope: authz.ActionScopeSystem, Summary: "manage events"}}, nil))
	s := module.NewServices()
	require.NoError(t, m.ProvideServices(s))
	var check func(context.Context, *gorm.DB, string, guid.ID) (bool, error)
	var record events.AdminAudit
	boundSecurity := false
	proof := authn.ReauthenticationConsumer(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil })
	store := idempotency.NewModule(h.runtime)
	require.NoError(t, module.Provide[events.AdminConnector](s, events.AdminConnector{
		Bind: func(r *authz.Registrar, c func(context.Context, *gorm.DB, string, guid.ID) (bool, error)) error {
			require.Same(t, m.registrar, r)
			check = c
			return nil
		},
		BindAudit: func(a events.AdminAudit) error { record = a; return nil },
		BindSecurity: func(p authn.ReauthenticationConsumer, i idempotency.Store) error {
			require.NotNil(t, p)
			require.Same(t, store, i)
			boundSecurity = true
			return nil
		},
	}))
	require.NoError(t, module.Provide[guid.Source](s, h.repository.ids))
	var recorded audit.DomainEvent
	require.NoError(t, module.Provide[audit.Appender](s, func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		require.NotNil(t, tx)
		recorded = e
		return nil
	}))
	require.NoError(t, module.Provide[authz.TokenVerifier](s, httpTokenVerifier{principalID: 11}))
	require.NoError(t, module.Provide[authn.ReauthenticationConsumer](s, proof))
	require.NoError(t, module.Provide[idempotency.Store](s, store))
	require.NoError(t, s.Seal())
	require.NoError(t, m.InjectServices(s))
	bindModuleTables(t, m, "sl_")
	require.True(t, boundSecurity)
	require.NotNil(t, record)
	_, err := h.repository.GrantPlatformAdministrator(t.Context(), 11)
	require.NoError(t, err)
	verify := func(want bool) {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			ok, err := check(t.Context(), tx, "system_event_manage", 11)
			require.NoError(t, err)
			require.Equal(t, want, ok)
			return nil
		}))
	}
	verify(true)
	require.NoError(t, h.db.Exec("DELETE FROM sl_iam_platform_administrators WHERE principal_id = ?", 11).Error)
	verify(false)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return record(t.Context(), tx, "events.replay_created", 0, "domain_event_subscriber", 0, map[string]any{"subscriber": "delivery.admin"})
	}))
	require.Equal(t, "delivery.admin", recorded.Detail["subscriber"])
}

func TestJobsAdministrationUsesCurrentPlatformAuthority(t *testing.T) {
	h := newRepositoryHarness(t, "sl_")
	seedSystemAccessFixture(t, h.db, "sl_", 11)
	instance := NewModule(h.runtime, enabledModuleConfig())
	require.NoError(t, instance.registrar.Registry().Contribute([]authz.Action{{Resource: "system_job", Verb: "manage", Scope: authz.ActionScopeSystem, Summary: "manage jobs"}}, nil))
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	var check func(context.Context, *gorm.DB, string, guid.ID) (bool, error)
	require.NoError(t, module.Provide[jobs.AdminConnector](services, jobs.AdminConnector{Bind: func(registrar *authz.Registrar, c func(context.Context, *gorm.DB, string, guid.ID) (bool, error)) error {
		require.Same(t, instance.registrar, registrar)
		check = c
		return nil
	}, BindAudit: func(jobs.AdminAudit) error { return nil }}))
	require.NoError(t, module.Provide[guid.Source](services, h.repository.ids))
	require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
	require.NoError(t, module.Provide[authz.TokenVerifier](services, httpTokenVerifier{principalID: 11}))
	require.NoError(t, services.Seal())
	require.NoError(t, instance.InjectServices(services))
	bindModuleTables(t, instance, "sl_")
	require.NotNil(t, check)
	_, err := h.repository.GrantPlatformAdministrator(t.Context(), 11)
	require.NoError(t, err)
	verify := func(want bool) {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			allowed, err := check(t.Context(), tx, "system_job_manage", 11)
			require.NoError(t, err)
			require.Equal(t, want, allowed)
			return nil
		}))
	}
	verify(true)
	require.NoError(t, h.db.Exec("DELETE FROM sl_iam_platform_administrators WHERE principal_id = ?", 11).Error)
	verify(false)
}
