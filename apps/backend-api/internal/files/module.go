package files

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

const Name = "files"

// Config belongs to the file module; identity has no storage path settings.
type Config struct {
	module.Config
	LocalRoot        string
	Backend          string
	Options          DriverConfig
	LegacyLocalRoots map[string]string
}

type Module struct {
	config       Config
	driver       Driver
	registry     *Registry
	runtime      *appenv.Runtime
	accounts     *AccountObjects
	bindings     *ResourceBindings
	resources    *AccountResourceFiles
	registrar    *authz.Registrar
	policy       core.EntityConstraintOn
	subjects     core.AuthorizationSubjectsOn
	references   core.AuthorizationReferencesOn
	ids          guid.Source
	appender     audit.Appender
	operations   *operationService
	workerMu     sync.Mutex
	workerCancel context.CancelFunc
	workerDone   chan struct{}
}

func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("files module requires runtime")
	}
	registry := NewRegistry()
	_ = registry.Register("local", func(c DriverConfig) (Driver, error) { return NewLocal(c["root"]) })
	return &Module{runtime: runtime, config: config, registry: registry}
}

// RegisterDriver adds a backend before service wiring. Unknown backends never
// fall back to local storage, and native providers need not expose S3 options.
func (m *Module) RegisterDriver(name string, factory DriverFactory) error {
	if m.driver != nil {
		return errors.New("files: drivers already wired")
	}
	return m.registry.Register(name, factory)
}
func (*Module) ModuleName() string           { return Name }
func (*Module) ModuleDependencies() []string { return []string{guid.Name, audit.Name, core.Name} }
func (m *Module) Enabled() bool              { return m != nil && m.config.Enabled }

func (m *Module) ProvideServices(services *module.Services) error {
	backend := m.config.Backend
	if backend == "" {
		backend = "local"
	}
	options := make(DriverConfig, len(m.config.Options)+1)
	for key, value := range m.config.Options {
		options[key] = value
	}
	if backend == "local" && options["root"] == "" {
		options["root"] = m.config.LocalRoot
		if options["root"] == "" {
			options["root"] = "data/files"
		}
	}
	driver, err := m.registry.Build(backend, options)
	if err != nil {
		return err
	}
	m.driver = driver
	if err := module.Provide[Driver](services, driver); err != nil {
		return err
	}
	m.accounts, err = NewAccountObjects(driver, backend, m.runtime.TablePrefix())
	if err != nil {
		return err
	}
	if err := module.Provide[*AccountObjects](services, m.accounts); err != nil {
		return err
	}
	m.bindings, err = NewResourceBindings(m.runtime.TablePrefix())
	if err != nil {
		return err
	}
	if err := module.Provide[*ResourceBindings](services, m.bindings); err != nil {
		return err
	}
	m.resources, err = NewAccountResourceFiles(m.accounts, m.bindings, driver, m.runtime.TablePrefix())
	if err != nil {
		return err
	}
	if err := module.Provide[*AccountResourceFiles](services, m.resources); err != nil {
		return err
	}
	if err := module.Provide[core.FileRootProvisioner](services, core.FileRootProvisioner(m.provisionOrganizationRoot)); err != nil {
		return fmt.Errorf("provide file root provisioner: %w", err)
	}
	if err := module.Provide[core.FileRootContentChecker](services, core.FileRootContentChecker(m.organizationRootHasContent)); err != nil {
		return fmt.Errorf("provide file root content checker: %w", err)
	}
	roots := make(map[string]string, len(m.config.LegacyLocalRoots))
	for key, value := range m.config.LegacyLocalRoots {
		roots[key] = value
	}
	return module.Provide[LegacyImporter](services, LegacyImporter(func(ctx context.Context, key string) error {
		return importLegacy(ctx, driver, roots, key)
	}))
}

func (m *Module) InjectServices(services *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return fmt.Errorf("resolve files authorization registrar: %w", err)
	}
	policy, err := module.Resolve[core.EntityConstraintOn](services)
	if err != nil {
		return fmt.Errorf("resolve files transaction policy: %w", err)
	}
	tenantPolicy, err := module.Resolve[core.TenantConstraintOn](services)
	if err != nil {
		return fmt.Errorf("resolve files transaction tenant policy: %w", err)
	}
	subjects, err := module.Resolve[core.AuthorizationSubjectsOn](services)
	if err != nil {
		return fmt.Errorf("resolve files authorization subjects: %w", err)
	}
	references, err := module.Resolve[core.AuthorizationReferencesOn](services)
	if err != nil {
		return fmt.Errorf("resolve files authorization references: %w", err)
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve files id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve files audit appender: %w", err)
	}
	actions := fileAuthorizationActions()
	for _, action := range actions {
		if _, exists := registrar.Registry().Find(action.Code); exists {
			return fmt.Errorf("register files permission %s: already registered", action.Code)
		}
	}
	for _, action := range actions {
		if err := registrar.Registry().Register(action); err != nil {
			return fmt.Errorf("register files permission %s: %w", action.Code, err)
		}
	}
	m.registrar, m.policy, m.subjects, m.references = registrar, policy, subjects, references
	m.ids, m.appender = ids, appender
	if m.resources != nil {
		if err := m.resources.Configure(ids, appender); err != nil {
			return err
		}
	}
	m.operations = &operationService{
		runtime: m.runtime, driver: m.driver, objects: m.accounts, ids: ids, policy: policy, tenantPolicy: tenantPolicy, subjects: subjects, references: references,
		appender: appender, budget: operationBudget{Nodes: 1000, ClosureRows: 100000}, timeout: 30 * time.Second,
		uploadTimeout: 15 * time.Minute, maxUploadBytes: 100 << 20,
	}
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if m.driver == nil {
		return ErrUnsupported
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.operations == nil || m.registrar == nil {
		return errOperationUnavailable
	}
	registerFileHTTP(api, m.operations, m.registrar)
	return nil
}

// Start runs bounded cleanup pages against the runtime's current writer.
func (m *Module) Start(ctx context.Context) error {
	if m.accounts == nil || m.bindings == nil || m.runtime == nil {
		return ErrUnsupported
	}
	m.workerMu.Lock()
	defer m.workerMu.Unlock()
	if m.workerCancel != nil {
		return nil
	}
	workerContext, cancel := context.WithCancel(ctx)
	m.workerCancel = cancel
	m.workerDone = make(chan struct{})
	go m.cleanupLoop(workerContext, m.workerDone)
	return nil
}

func (m *Module) cleanupLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var after, operationAfter, bindingAfter, resourceAfter guid.ID
	for {
		if m.runtime.Database() != nil && m.runtime.Database().Write() != nil {
			workContext, cancel := context.WithTimeout(ctx, 30*time.Second)
			tree := directoryTree{prefix: m.accounts.prefix}
			nextOperation, operationErr := tree.expireOperations(workContext, m.runtime.Database().Write(), time.Now(), operationAfter, 100)
			operationAfter = nextOperation
			if operationErr != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "file operation expiry will retry", "error", operationErr)
			}
			nextBinding, bindingErr := m.bindings.Expire(workContext, m.runtime.Database().Write(), time.Now(), bindingAfter, 100)
			bindingAfter = nextBinding
			if bindingErr != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "file resource binding expiry will retry", "error", bindingErr)
			}
			nextResource, resourceErr := m.resources.PurgeOrphaned(workContext, m.runtime.Database().Write(), time.Now(), resourceAfter, 100)
			resourceAfter = nextResource
			if resourceErr != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "orphaned resource file cleanup will retry", "error", resourceErr)
			}
			next, err := m.accounts.CollectAccountObjects(workContext, m.runtime.Database().Write(), time.Now(), after, 100)
			cancel()
			after = next
			if err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "account file cleanup will retry", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Stop waits for the owned worker; storage work honors the shutdown context.
func (m *Module) Stop(ctx context.Context) error {
	m.workerMu.Lock()
	defer m.workerMu.Unlock()
	if m.workerCancel == nil {
		return nil
	}
	m.workerCancel()
	select {
	case <-m.workerDone:
		m.workerCancel, m.workerDone = nil, nil
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var (
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
)
