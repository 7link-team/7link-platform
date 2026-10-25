// Package performance owns owner-managed assessment configurations and their immutable history.
package performance

//go:generate go run ./dbgen
import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	pi "7link.com/platform/internal/performance/i18n"
	"7link.com/platform/internal/performance/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
)

const Name = "performance"

type Config struct{ module.Config }
type Module struct {
	runtime     *appenv.Runtime
	config      Config
	names       query.TableNames
	policy      core.TenantConstraintOn
	principals  identity.PrincipalStateStore
	ids         guid.Source
	audit       audit.Appender
	events      events.Publisher
	idem        idempotency.Store
	registrar   *authz.Registrar
	evidence    EvidenceReader
	initialized bool
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	return &Module{runtime: rt, config: c, names: query.BindNames(rt.TablePrefix())}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{core.Name, identity.Name, audit.Name, events.Name, guid.Name, idempotency.Name, jobs.Name}
}
func (m *Module) InjectServices(s *module.Services) error {
	var e error
	if m.policy, e = module.Resolve[core.TenantConstraintOn](s); e != nil {
		return e
	}
	if m.principals, e = module.Resolve[identity.PrincipalStateStore](s); e != nil {
		return e
	}
	if m.ids, e = module.Resolve[guid.Source](s); e != nil {
		return e
	}
	if m.audit, e = module.Resolve[audit.Appender](s); e != nil {
		return e
	}
	if m.events, e = module.Resolve[events.Publisher](s); e != nil {
		return e
	}
	if m.idem, e = module.Resolve[idempotency.Store](s); e != nil {
		return e
	}
	if m.registrar, e = module.Resolve[*authz.Registrar](s); e != nil {
		return e
	}
	if e = m.registrar.Registry().Contribute([]authz.Action{{Resource: "performance", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "Manage owner assessment schemes"}}, nil); e != nil {
		return e
	}
	catalog, e := module.Resolve[events.Catalog](s)
	if e != nil {
		return e
	}
	return catalog.AddTypes(Name,
		events.TypeSpec{Name: "performance.scheme_revised", SchemaVersion: 1, SubjectKind: "performance_scheme", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "performance.report_generated", SchemaVersion: 1, SubjectKind: "performance_report", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "performance.report_recalculated", SchemaVersion: 1, SubjectKind: "performance_report", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "performance.report_calibrated", SchemaVersion: 1, SubjectKind: "performance_report", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "performance.report_confirmed", SchemaVersion: 1, SubjectKind: "performance_report", Realtime: events.RealtimeSignal})
}
func (m *Module) Initialize(context.Context) error {
	if m.ids == nil || m.policy == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.audit == nil || m.events == nil || m.idem == nil {
		return ErrUnavailable
	}
	if err := pi.Register(); err != nil {
		return err
	}
	m.initialized = true
	return nil
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ module.HTTPRegistrar           = (*Module)(nil)
)
