package guid

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	guidi18n "7link.com/platform/internal/infra/guid/i18n"
	guidv1 "7link.com/platform/internal/infra/guid/proto/gen/go/guid/v1"
	"7link.com/platform/internal/infra/wuid"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"google.golang.org/grpc"
)

var ErrUnavailable = errors.New("guid: generator unavailable")

const Name = guidi18n.Namespace

type Module struct {
	source    wuid.Source
	generator *Generator
	active    atomic.Bool
}

// Source is the GUID service contract exposed to dependent modules.
type Source interface {
	Next() (int64, error)
}

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string { return []string{platformi18n.Name, wuid.Name} }

var (
	_ module.Initializer     = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.Stopper         = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
	_ module.GRPCRegistrar   = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ Source                 = (*Module)(nil)
)

func NewModule() *Module {
	return &Module{}
}

func (m *Module) ProvideServices(services *module.Services) error {
	return module.Provide[Source](services, m)
}

func (m *Module) InjectServices(services *module.Services) error {
	source, err := module.Resolve[wuid.Source](services)
	if err != nil {
		return err
	}
	m.source = source
	m.generator = newManagedGenerator(source.Current)
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := guidi18n.Register(); err != nil {
		return fmt.Errorf("register i18n: %w", err)
	}
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m.generator == nil {
		return errors.New("guid: worker ID source was not injected")
	}
	m.active.Store(true)
	if err := m.generator.Ready(); err != nil {
		if errors.Is(err, wuid.ErrUnavailable) {
			return nil
		}
		return fmt.Errorf("initialize generator: %w", err)
	}
	lease, err := m.source.Current()
	if err != nil {
		return nil
	}
	slog.InfoContext(ctx, "guid generator ready", "worker_id", lease.WorkerID)
	return nil
}

// Next returns one ID from this module's generator.
func (m *Module) Next() (int64, error) {
	if !m.active.Load() || m.generator == nil {
		return 0, ErrUnavailable
	}
	return m.generator.Next()
}

func (m *Module) CheckHealth(context.Context) error {
	if !m.active.Load() || m.generator == nil {
		return ErrUnavailable
	}
	if err := m.generator.Ready(); err != nil {
		return errors.Join(ErrUnavailable, err)
	}
	return nil
}

func (m *Module) Stop(context.Context) error {
	m.active.Store(false)
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	registerHTTP(api, m.Next)
	return nil
}

func (m *Module) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	guidv1.RegisterGuidServiceServer(registrar, &grpcServer{next: m.Next})
}
