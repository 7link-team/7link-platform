// Package governance owns business authorization and managed role seeding.
package governance

//go:generate go run ./dbgen

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// Name is distinct from the existing IAM access-governance module.
const Name = "business_governance"

// JobNamespace preserves the business job names frozen by spec08. The module
// lifecycle name differs because IAM already owns the governance module name.
const JobNamespace = "governance"

var ErrRoleNotSeeded = errors.New("business governance role is missing or conflicted")

type Config struct{ module.Config }

// SeededRoles resolves business role keys without exposing governance storage.
// EnsureTenant owns its transaction and must run before a caller's business
// transaction. RoleIDOn reads that caller's transaction; callers authorize their
// business operation and retain responsibility for role/subject compatibility.
type SeededRoles struct {
	EnsureTenant func(context.Context, guid.ID) error
	RoleIDOn     func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error)
}

// Module owns governance records and capabilities.
type Module struct {
	runtime       *appenv.Runtime
	config        Config
	seed          *seedService
	tenants       organization.TenantEnumerator
	queue         jobs.Queue
	explain       core.AuthorizationExplanation
	systemActions systemActionRegistry
	policies      policyRegistry
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{runtime: rt, config: config, seed: &seedService{names: query.BindNames(rt.TablePrefix()), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{guid.Name, core.Name, audit.Name, dlock.Name, organization.Name, jobs.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[ProjectCreationAuthority](services, ProjectCreationAuthority(m.projectCreationAuthority)); err != nil {
		return err
	}
	if err := module.Provide[SeededRoles](services, SeededRoles{EnsureTenant: m.ensureTenant, RoleIDOn: m.roleIDOn}); err != nil {
		return err
	}
	if err := module.Provide[SystemActions](services, &m.systemActions); err != nil {
		return err
	}
	return module.Provide[PolicyRegistry](services, &m.policies)
}
func (m *Module) InjectServices(services *module.Services) error {
	if m == nil || m.seed == nil {
		return ErrUnavailable
	}
	var err error
	if m.seed.ids, err = module.Resolve[guid.Source](services); err != nil {
		return err
	}
	if m.seed.roles, err = module.Resolve[core.RoleSeeder](services); err != nil {
		return err
	}
	if m.explain, err = module.Resolve[core.AuthorizationExplanation](services); err != nil {
		return err
	}
	if m.seed.audit, err = module.Resolve[audit.Appender](services); err != nil {
		return err
	}
	if m.seed.acquire, err = module.Resolve[dlock.TransactionLease](services); err != nil {
		return err
	}
	if m.tenants, err = module.Resolve[organization.TenantEnumerator](services); err != nil {
		return err
	}
	if m.queue, err = module.Resolve[jobs.Queue](services); err != nil {
		return err
	}
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return err
	}
	m.seed.registry = registrar.Registry()
	return nil
}
func (m *Module) Initialize(context.Context) error {
	if m == nil || m.seed == nil || m.seed.ids == nil || m.seed.roles.EnsureRoleOn == nil || m.seed.roles.GrantPermissionOn == nil || m.seed.roles.GrantConditionalPermissionOn == nil || m.seed.roles.SeedAdministratorMembersOn == nil || m.seed.audit == nil || m.seed.acquire == nil || m.seed.registry == nil || m.explain == nil {
		return ErrUnavailable
	}
	if err := m.systemActions.seal(m.seed.registry); err != nil {
		return err
	}
	return m.policies.seal(m.seed.registry)
}

// JobHandlers contributes the platform-wide role reconciliation sweep.
func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{Kind: JobNamespace + ".seed_reconcile", Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID != 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		return m.reconcileTenants(ctx)
	}}}
}

// JobSchedules declares hourly tenant role reconciliation. The platform sweep
// owns tenant paging itself rather than using the active-tenant fanout handler.
func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: JobNamespace + ".seed_reconcile", Kind: JobNamespace + ".seed_reconcile", Interval: time.Hour, TenantScope: jobs.ScopePlatform}}
}

// Start persists the startup sweep. Every replica may submit a sweep; the
// shared concurrency key serializes execution and the seed ledger makes replay
// safe. A failed enqueue fails startup instead of silently skipping seeding.
func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.queue == nil {
		return ErrUnavailable
	}
	_, err := m.queue.Enqueue(ctx, nil, jobs.EnqueueRequest{Kind: JobNamespace + ".seed_reconcile", ConcurrencyKey: JobNamespace + ".seed_reconcile"})
	return err
}
func (m *Module) ensureTenant(ctx context.Context, tenantID guid.ID) error {
	if err := m.Initialize(ctx); err != nil {
		return err
	}
	if m.runtime == nil || m.runtime.Database() == nil {
		return ErrUnavailable
	}
	return m.seed.apply(ctx, m.runtime.Database().Write(), tenantID, RoleManifestVersion, defaultRoleManifest())
}

// reconcileTenants is the shared startup/scheduled sweep. Each tenant commits
// independently; retrying a failed sweep is safe because seeding is idempotent.
// A tenant held by another seeder is skipped only for this background sweep.
// Synchronous first access still receives ErrNotAcquired from ensureTenant.
func (m *Module) reconcileTenants(ctx context.Context) error {
	if m == nil || m.tenants == nil {
		return ErrUnavailable
	}
	const pageSize = 100
	var after guid.ID
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tenants, err := m.tenants(ctx, after, pageSize)
		if err != nil {
			return fmt.Errorf("enumerate tenants for role seeding: %w", err)
		}
		for _, tenantID := range tenants {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := m.ensureTenant(ctx, tenantID); err != nil && !errors.Is(err, dlock.ErrNotAcquired) {
				return fmt.Errorf("seed tenant %s: %w", tenantID, err)
			}
		}
		if len(tenants) < pageSize {
			return nil
		}
		after = tenants[len(tenants)-1]
	}
}
func (m *Module) roleIDOn(ctx context.Context, tx *gorm.DB, tenantID guid.ID, key string) (guid.ID, error) {
	if m == nil || m.seed == nil || m.seed.names.RoleSeeds.IsZero() {
		return 0, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenantID <= 0 || key == "" {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	table := query.BindRoleSeeds(tx, m.seed.names)
	row, err := table.WithContext(ctx).Where(table.TenantID.Eq(tenantID), table.RoleKey.Eq(key)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrRoleNotSeeded
	}
	if err != nil {
		return 0, err
	}
	// Detaching a role from upgrades does not revoke its business identity.
	if row.SeedConflict || row.RoleID <= 0 {
		return 0, ErrRoleNotSeeded
	}
	return row.RoleID, nil
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.Starter                 = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ jobs.HandlerProvider           = (*Module)(nil)
	_ jobs.ScheduleProvider          = (*Module)(nil)
)
