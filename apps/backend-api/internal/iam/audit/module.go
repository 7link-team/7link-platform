package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"

	auditi18n "7link.com/platform/internal/iam/audit/i18n"
	"7link.com/platform/internal/infra/guid"
	healthservice "7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

var ErrUnavailable = errors.New("audit service unavailable")

// Module owns the IAM append-only audit chain, retention policy, anchoring,
// HTTP operations, and the Appender capability consumed by other IAM modules.
type Module struct {
	principalLabels PrincipalLabelLookup
	service         *Service
	runtime         *appenv.Runtime
	registrar       *authz.Registrar
	config          Config
	ids             guid.Source
	workerMu        sync.Mutex
	workerCancel    context.CancelFunc
	workerDone      chan struct{}
}

func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("audit module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string { return []string{platformi18n.Name, guid.Name} }

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[Appender](services, Appender(m.appendDomain)); err != nil {
		return fmt.Errorf("provide audit appender: %w", err)
	}
	if err := module.Provide[EventStreamReader](services, EventStreamReader(m.readStream)); err != nil {
		return err
	}
	if err := module.Provide[ErasureRecorder](services, ErasureRecorder(m.recordErasure)); err != nil {
		return fmt.Errorf("provide erasure recorder: %w", err)
	}
	if err := m.provideBusinessEvents(services); err != nil {
		return err
	}
	return module.Provide[PrincipalLabelConnector](services, PrincipalLabelConnector{Bind: func(lookup PrincipalLabelLookup) error {
		if lookup == nil || m.principalLabels != nil {
			return errors.New("audit principal label lookup must be supplied once")
		}
		m.principalLabels = lookup
		return nil
	}})
}

func (m *Module) InjectServices(services *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return fmt.Errorf("resolve audit authorization registrar: %w", err)
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve audit id generator: %w", err)
	}
	m.registrar, m.ids = registrar, ids
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := auditi18n.Register(); err != nil {
		return fmt.Errorf("register audit i18n: %w", err)
	}
	if m == nil {
		return errors.New("initialize audit: module is required")
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.registrar == nil || m.ids == nil {
		return errors.New("initialize audit: runtime database, authz registrar, and id generator are required")
	}
	m.service = NewService(m.runtime, m.ids)
	m.service.principalLabels = m.principalLabels
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || (!m.config.Anchor.Enabled && !m.config.Archive.Enabled && !m.config.ErasureLedger.Enabled) {
		return nil
	}
	m.workerMu.Lock()
	defer m.workerMu.Unlock()
	if m.workerDone != nil {
		return nil
	}
	if m.service == nil {
		return ErrUnavailable
	}
	store, err := NewAnchorStore(m.config.Anchor)
	if err != nil {
		return fmt.Errorf("audit anchor configuration: %w", err)
	}
	service := NewService(m.runtime, m.ids)
	service.principalLabels = m.principalLabels
	if m.config.Anchor.Enabled {
		service.anchor = store
	}
	if m.config.Anchor.Enabled && m.config.Anchor.SigningKey != "" {
		signer, err := NewRootSigner(m.config.Anchor.SigningKey)
		if err != nil {
			return fmt.Errorf("audit anchor signing key: %w", err)
		}
		service.signer = signer
	}
	if m.config.Archive.Enabled {
		if err := validateArchiveWorker(m.config.Archive); err != nil {
			return err
		}
		var existingIDs []guid.ID
		db := service.repo.writer()
		if err := db.WithContext(ctx).Table(service.repo.tables(db).ArchiveJobs.TableName()).Limit(1).Pluck("id", &existingIDs).Error; err != nil {
			return fmt.Errorf("audit archive schema is unavailable; apply audit v3 migration: %w", err)
		}
		service.archive = store
	}
	if m.config.ErasureLedger.Enabled {
		if err := validateErasureLedgerConfig(m.config.ErasureLedger); err != nil {
			return err
		}
		service.erasureLedger = NewErasureLedger(store)
		service.erasureTimeout = m.config.ErasureLedger.Timeout
		checkCtx, cancel := context.WithTimeout(ctx, service.erasureTimeout)
		err := service.erasureLedger.Check(checkCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("audit erasure ledger: %w", err)
		}
	}
	m.service = service
	if m.config.Archive.Enabled {
		runCtx, cancel := context.WithCancel(ctx)
		m.workerCancel, m.workerDone = cancel, make(chan struct{})
		go m.runArchives(runCtx, service, m.workerDone)
	}
	return nil
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.service == nil || m.service.repo == nil || m.service.repo.writer() == nil {
		return ErrUnavailable
	}
	return nil
}

// HealthCheckers reports optional erasure capability without removing healthy
// authentication and audit services from readiness during an object-store outage.
func (m *Module) HealthCheckers() []healthservice.Checker {
	checks := []healthservice.Checker{{Name: Name, Check: m.CheckHealth}}
	if m != nil && m.config.ErasureLedger.Enabled {
		checks = append(checks, healthservice.Checker{Name: Name + "-erasure-ledger", Optional: true, Check: m.checkErasureHealth})
	}
	return checks
}

func (m *Module) checkErasureHealth(ctx context.Context) error {
	if m.service == nil || m.service.erasureLedger == nil || m.service.erasureLedger.store == nil {
		return ErrErasureLedgerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, m.config.ErasureLedger.Timeout)
	defer cancel()
	store := m.service.erasureLedger.store
	// Read configuration afresh; ensureBucket caches success and its startup
	// probe writes an object. Neither is a runtime health probe.
	lock, _, _, _, err := store.client.GetObjectLockConfig(ctx, store.bucket)
	if err != nil {
		return erasureLedgerStoreError(err)
	}
	if lock != "Enabled" {
		return ErrErasureLedgerUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return errors.New("register audit: module is unavailable")
	}
	if m.service == nil {
		return errors.New("register audit: service is unavailable")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

// readStream adapts the service to EventStreamReader so a follower resolves a
// function rather than this module.
func (m *Module) readStream(ctx context.Context, tenantID guid.ID, afterSequence int64, limit int) ([]StreamEvent, error) {
	if m == nil || m.service == nil {
		return nil, ErrUnavailable
	}
	return m.service.ReadStream(ctx, tenantID, afterSequence, limit)
}

func (m *Module) appendDomain(ctx context.Context, db *gorm.DB, event DomainEvent) error {
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	return m.service.DomainAppender()(ctx, db, event)
}

func (m *Module) recordErasure(ctx context.Context, obligation ErasureObligation) error {
	if m == nil || !m.config.ErasureLedger.Enabled {
		return ErrErasureLedgerDisabled
	}
	if m.service == nil || m.service.erasureLedger == nil {
		return ErrErasureLedgerUnavailable
	}
	writeCtx, cancel := context.WithTimeout(ctx, m.service.erasureTimeout)
	defer cancel()
	return m.service.erasureLedger.Put(writeCtx, obligation)
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
