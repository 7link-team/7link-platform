package provisioning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	provisioningi18n "7link.com/platform/internal/iam/provisioning/i18n"
	"7link.com/platform/internal/iam/provisioning/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

var ErrUnavailable = errors.New("provisioning service unavailable")

type Module struct {
	service         *Service
	runtime         *appenv.Runtime
	registrar       *authz.Registrar
	config          Config
	ids             guid.Source
	appender        audit.Appender
	identities      identity.IdentityProvisioner
	actors          identity.ServiceAccountValidator
	fields          authz.FieldPermissionChecker
	groups          organization.GroupProvisioner
	syncCancel      context.CancelFunc
	syncDone        chan struct{}
	syncMu          sync.Mutex
	syncCursor      guid.ID
	syncRuns        atomic.Int64
	syncFailures    atomic.Int64
	syncLastSuccess atomic.Int64
	// foreign is the one holder of the table names the modules that own them
	// published to provisioning. The repository reads through it, so a name
	// that was never published fails the statement instead of reaching the
	// database.
	foreign foreignTables
}

func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("provisioning module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, identity.Name, organization.Name, core.Name}
}

// ProvideServices publishes the module itself, so the capability resolves the
// service at call time. The service only exists after Initialize, and the
// provider phase runs before that.
func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[SecretRewrapper](services, m); err != nil {
		return fmt.Errorf("provide provisioning secret rewrapper: %w", err)
	}
	return nil
}

func (m *Module) InjectServices(services *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning authorization registrar: %w", err)
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning audit appender: %w", err)
	}
	identities, err := module.Resolve[identity.IdentityProvisioner](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning identity provisioner: %w", err)
	}
	groups, err := module.Resolve[organization.GroupProvisioner](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning group provisioner: %w", err)
	}
	actors, err := module.Resolve[identity.ServiceAccountValidator](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning service account validator: %w", err)
	}
	m.registrar, m.ids, m.appender, m.identities, m.groups = registrar, ids, appender, identities, groups
	m.actors = actors
	fields, err := module.Resolve[core.AuthorizationEvaluator](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning field evaluator: %w", err)
	}
	m.fields = fields
	accessTables, err := module.Resolve[core.AccessTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning access tables: %w", err)
	}
	principalTables, err := module.Resolve[identity.PrincipalTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning principal tables: %w", err)
	}
	directoryTables, err := module.Resolve[organization.DirectoryTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve provisioning directory tables: %w", err)
	}
	if err := m.foreign.bind(accessTables, principalTables, directoryTables); err != nil {
		return fmt.Errorf("bind provisioning foreign tables: %w", err)
	}
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if m == nil {
		return errors.New("initialize provisioning: module is required")
	}
	if err := provisioningi18n.Register(); err != nil {
		return fmt.Errorf("register provisioning i18n: %w", err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil ||
		m.registrar == nil || m.ids == nil || m.appender == nil || m.identities == nil || m.groups == nil || m.actors == nil {
		return errors.New("initialize provisioning: runtime database, authz registrar, id generator, audit appender, identity provisioner, group provisioner, and service account validator are required")
	}
	// Checked here rather than in every statement: injection has run, so an
	// owner that published nothing is a deployment that must not start.
	if !m.foreign.bound() {
		return errors.New("initialize provisioning: the core, identity, and organization modules must publish the table names provisioning joins")
	}
	keys, err := ResolveKeyring(m.config)
	if err != nil {
		return fmt.Errorf("resolve provisioning encryption key: %w", err)
	}
	outbound, err := ResolveOutboundPolicy(m.config)
	if err != nil {
		return fmt.Errorf("resolve provisioning outbound policy: %w", err)
	}
	m.service = NewService(m.runtime, m.appender, m.ids, m.identities, m.groups, m.actors, m.fields, m.config, keys, outbound)
	m.service.repo.foreign = &m.foreign
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.config.SyncInterval <= 0 {
		return nil
	}
	if m.service == nil {
		return ErrUnavailable
	}
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	if m.syncCancel != nil {
		return nil
	}
	workerContext, cancel := context.WithCancel(ctx)
	m.syncCancel = cancel
	m.syncDone = make(chan struct{})
	go m.syncLoop(workerContext, m.syncDone)
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.syncMu.Lock()
	cancel, done := m.syncCancel, m.syncDone
	m.syncCancel, m.syncDone = nil, nil
	m.syncMu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Module) syncLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(m.config.SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reconcileAll(ctx)
		}
	}
}

func (m *Module) reconcileAll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, syncBatchTimeout)
	defer cancel()
	m.syncRuns.Add(1)
	var targets []targetRow
	scimTargets := query.BindNames(m.service.repo.prefix()).Targets.String()
	tenants := m.service.repo.foreign.directory.Tenants.String()
	err := m.service.repo.writer().WithContext(ctx).Table(scimTargets).
		Where("status = ? AND retiring = ? AND id > ? AND sync_after_ms <= ?", TargetActive, false, m.syncCursor, m.service.now().UnixMilli()).
		Where("tenant_id IN (?)", m.service.repo.writer().Table(tenants).Select("id").Where("status = 'active'")).
		Order("id ASC").Limit(syncTargetPageSize).Find(&targets).Error
	if err != nil {
		m.syncFailures.Add(1)
		slog.Warn("scim sync: list targets failed", "error", err)
		return
	}
	failed := false
	for _, target := range targets {
		if ctx.Err() != nil {
			failed = true
			break
		}
		m.syncCursor = target.ID
		count, err := m.service.SyncTarget(ctx, target.TenantID, target.ID)
		if err != nil {
			failed = true
			slog.Warn("scim sync: target reconcile failed", "target", target.ID, "error", err)
			continue
		}
		if count > 0 {
			slog.Info("scim sync: resources pushed", "target", target.ID, "count", count)
		}
	}
	if len(targets) < syncTargetPageSize && ctx.Err() == nil {
		m.syncCursor = 0
	}
	if failed {
		m.syncFailures.Add(1)
	} else {
		m.syncLastSuccess.Store(m.service.now().Unix())
	}
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.service == nil || m.service.repo == nil || m.service.repo.writer() == nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return errors.New("register provisioning: module is unavailable")
	}
	if m.service == nil {
		return errors.New("register provisioning: service is unavailable")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Enabler         = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.Stopper         = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
)
