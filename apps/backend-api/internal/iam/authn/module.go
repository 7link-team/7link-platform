package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	authni18n "7link.com/platform/internal/iam/authn/i18n"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// Module owns authentication credentials, sessions, factors, challenges,
// browser authentication HTTP operations, and their worker lifecycle.
type Module struct {
	*Service
	runtime             *appenv.Runtime
	config              Config
	registrationCreator RegistrationPrincipalCreator
	ids                 guid.Source
	appender            audit.Appender
}

// Config separates module activation from the authentication service's
// operational settings while allowing the composition root to pass one value.
type Config struct {
	module.Config
	Service authnext.Config
}

// NewModule builds the authn module from construction-owned inputs. Shared
// capabilities are resolved during module wiring.
func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("authn module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

// Enabled is evaluated before Initialize, so it must use construction-time
// configuration rather than the embedded service created during Initialize.
func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[WebService](services, m); err != nil {
		return fmt.Errorf("provide authn web service: %w", err)
	}
	if err := module.Provide[InvitationCredentials](services, m); err != nil {
		return fmt.Errorf("provide authn invitation credentials: %w", err)
	}
	if err := module.Provide[authz.TokenVerifier](services, m); err != nil {
		return fmt.Errorf("provide authn token verifier: %w", err)
	}
	return nil
}

func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve authn id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve authn audit appender: %w", err)
	}
	principalCreator, err := module.Resolve[identity.RegistrationPrincipalCreator](services)
	if err != nil {
		return fmt.Errorf("resolve authn registration principal creator: %w", err)
	}
	m.ids = ids
	m.appender = appender
	m.registrationCreator = func(
		ctx context.Context,
		db *gorm.DB,
		email, passwordHash, displayName string,
		now time.Time,
		_ guid.Source,
	) (guid.ID, error) {
		id, err := principalCreator(ctx, db, email, passwordHash, displayName, now)
		switch {
		case errors.Is(err, identity.ErrLoginConflict):
			return 0, authnext.ErrRegistrationConflict
		case errors.Is(err, identity.ErrInvalid):
			return 0, authnext.ErrInvalidRegistration
		default:
			return id, err
		}
	}
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := authni18n.Register(); err != nil {
		return fmt.Errorf("register authn i18n: %w", err)
	}
	if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil {
		return fmt.Errorf("initialize authn: runtime, id generator, and audit appender are required")
	}
	repository := NewRepository(m.runtime, m.ids, m.appender)
	service, err := NewWebService(
		m.config.Service,
		repository,
		WithRegistrationPrincipalCreator(m.registrationCreator),
	)
	if err != nil {
		return fmt.Errorf("initialize authn service: %w", err)
	}
	m.Service = service
	return nil
}

// Authenticate is explicit so the module can be published as a verifier
// during Wire without dereferencing its not-yet-initialized embedded service.
func (m *Module) Authenticate(ctx context.Context, authorization, cookie string) (*authnext.Claims, error) {
	if m == nil || m.Service == nil {
		return nil, authnext.ErrUnavailable
	}
	return m.Service.Authenticate(ctx, authorization, cookie)
}

func (m *Module) ValidateCSRF(method, origin, cookie, authorization string) error {
	if m == nil || m.Service == nil {
		return authnext.ErrUnavailable
	}
	return m.Service.ValidateCSRF(method, origin, cookie, authorization)
}

func (m *Module) SessionCookieName() string {
	if m == nil || m.Service == nil {
		return ""
	}
	return m.Service.SessionCookieName()
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.Service == nil || m.repo == nil || m.repo.writer() == nil {
		return authnext.ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.Service == nil {
		return fmt.Errorf("register authn: service is unavailable")
	}
	RegisterHTTP(api, m.Service, m.Service)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.Service == nil {
		return fmt.Errorf("start authn: service is unavailable")
	}
	return m.StartNotificationWorker(ctx)
}

func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.Service == nil {
		return nil
	}
	return m.StopNotificationWorker(ctx)
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Enabler         = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.Stopper         = (*Module)(nil)
	_ WebService             = (*Module)(nil)
	_ InvitationCredentials  = (*Module)(nil)
	_ authz.TokenVerifier    = (*Module)(nil)
)
