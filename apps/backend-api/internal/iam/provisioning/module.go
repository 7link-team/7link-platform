package provisioning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	provisioningi18n "7link.com/platform/internal/iam/provisioning/i18n"
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
	groups          organization.GroupProvisioner
	declarationOnly bool
	syncCancel      context.CancelFunc
	syncDone        chan struct{}
	syncMu          sync.Mutex
}

func NewModule(runtime *appenv.Runtime, registrar *authz.Registrar, config Config) *Module {
	if runtime == nil || registrar == nil || registrar.Registry() == nil || registrar.IsDeclarationOnly() {
		panic("provisioning module requires runtime and authz registrar")
	}
	return &Module{runtime: runtime, registrar: registrar, config: config}
}

func NewDeclarationOnlyModule(registrar *authz.Registrar) *Module {
	if registrar == nil || registrar.Registry() == nil || !registrar.IsDeclarationOnly() {
		panic("provisioning declaration module requires a declaration-only authz registrar")
	}
	return &Module{registrar: registrar, declarationOnly: true}
}

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, identity.Name, organization.Name}
}

func (m *Module) InjectServices(services *module.Services) error {
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
	m.ids, m.appender, m.identities, m.groups = ids, appender, identities, groups
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if m == nil {
		return errors.New("initialize provisioning: module is required")
	}
	if err := provisioningi18n.Register(); err != nil {
		return fmt.Errorf("register provisioning i18n: %w", err)
	}
	if m.declarationOnly {
		return nil
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil ||
		m.registrar == nil || m.ids == nil || m.appender == nil || m.identities == nil || m.groups == nil {
		return errors.New("initialize provisioning: runtime database, authz registrar, id generator, audit appender, identity provisioner, and group provisioner are required")
	}
	key, err := ResolveEncryptionKey(m.config)
	if err != nil {
		return fmt.Errorf("resolve provisioning encryption key: %w", err)
	}
	m.service = NewService(m.runtime, m.appender, m.ids, m.identities, m.groups, m.config, key)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.declarationOnly || m.config.SyncInterval <= 0 {
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
	var tenants []guid.ID
	err := m.service.repo.writer().WithContext(ctx).Table(m.service.repo.table("iam_tenants")).
		Where("status = 'active'").Pluck("id", &tenants).Error
	if err != nil {
		slog.Warn("scim sync: list tenants failed", "error", err)
		return
	}
	for _, tenantID := range tenants {
		targets, err := m.service.ListTargets(ctx, tenantID)
		if err != nil {
			slog.Warn("scim sync: list targets failed", "tenant", tenantID, "error", err)
			continue
		}
		for _, target := range targets {
			if target.Status != TargetActive {
				continue
			}
			targetID, err := guid.Parse(target.ID)
			if err != nil {
				slog.Warn("scim sync: invalid target identifier", "target", target.ID, "error", err)
				continue
			}
			count, err := m.service.SyncTarget(ctx, tenantID, targetID)
			if err != nil {
				slog.Warn("scim sync: target reconcile failed", "target", target.ID, "error", err)
				continue
			}
			if count > 0 {
				slog.Info("scim sync: resources pushed", "target", target.ID, "count", count)
			}
		}
	}
}

func (m *Module) CheckHealth(context.Context) error {
	if m != nil && m.declarationOnly {
		return nil
	}
	if m == nil || m.service == nil || m.service.repo == nil || m.service.repo.writer() == nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return errors.New("register provisioning: module is unavailable")
	}
	if !m.declarationOnly && m.service == nil {
		return errors.New("register provisioning: service is unavailable")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.Stopper         = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
)
