package files

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
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
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }

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
	roots := make(map[string]string, len(m.config.LegacyLocalRoots))
	for key, value := range m.config.LegacyLocalRoots {
		roots[key] = value
	}
	return module.Provide[LegacyImporter](services, LegacyImporter(func(ctx context.Context, key string) error {
		return importLegacy(ctx, driver, roots, key)
	}))
}

func (m *Module) Initialize(context.Context) error {
	if m.driver == nil {
		return ErrUnsupported
	}
	return nil
}

// Start runs bounded cleanup pages against the runtime's current writer.
func (m *Module) Start(ctx context.Context) error {
	if m.accounts == nil || m.runtime == nil {
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
	var after, operationAfter guid.ID
	for {
		if m.runtime.Database() != nil && m.runtime.Database().Write() != nil {
			workContext, cancel := context.WithTimeout(ctx, 30*time.Second)
			tree := directoryTree{prefix: m.accounts.prefix}
			nextOperation, operationErr := tree.expireOperations(workContext, m.runtime.Database().Write(), time.Now(), operationAfter, 100)
			operationAfter = nextOperation
			if operationErr != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "file operation expiry will retry", "error", operationErr)
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
