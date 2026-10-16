// Package delivery owns projects, work items and delivery business records.
package delivery

//go:generate go run ./dbgen

import (
	"context"
	"fmt"
	"log/slog"

	"7link.com/platform/internal/collab"
	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
)

const Name = "delivery"

type Config struct{ module.Config }

// ProjectProvisioningConnector lets the composition root bind optional
// provisioning capabilities before initialization. Authentication/authorization
// remains a required IAM dependency; unavailable provisioning never bypasses it.
type ProjectProvisioningConnector struct {
	Bind func(core.EntityProvisioner, core.EntityRoleBindingStore, collab.ChannelProvisioner) error
}

// Module owns the delivery schema and business capabilities.
type Module struct {
	runtime           *appenv.Runtime
	config            Config
	service           *projectService
	ids               guid.Source
	roles             governance.SeededRoles
	idempotency       idempotency.Store
	createAuthority   governance.ProjectCreationAuthority
	members           core.ActiveMemberChecker
	agents            fleet.AgentDirectory
	sources           intake.IntakeReader
	principals        identity.PrincipalStateStore
	registrar         *authz.Registrar
	checker           core.AuthorizationEvaluator
	entityOwner       core.EntityOwnerResolver
	projectAccess     core.AccessTableNames
	listConstraint    core.TenantConstraint
	provisioningBound bool
	initialized       bool
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{runtime: rt, config: config, service: &projectService{names: query.BindNames(rt.TablePrefix())}}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, idempotency.Name, core.Name, identity.Name, fleet.Name, intake.Name, audit.Name, events.Name, governance.Name}
}
func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[core.EntityTypeReservations](services, func() []string { return []string{"project"} }); err != nil {
		return err
	}
	return module.Provide[ProjectProvisioningConnector](services, ProjectProvisioningConnector{Bind: m.bindProvisioning})
}
func (m *Module) bindProvisioning(entities core.EntityProvisioner, roles core.EntityRoleBindingStore, channels collab.ChannelProvisioner) error {
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	if m.provisioningBound || m.initialized {
		return ErrInvalidArgument
	}
	m.service.entities, m.service.roles, m.service.channels = entities, roles, channels
	m.provisioningBound = true
	return nil
}
func (m *Module) InjectServices(services *module.Services) error {
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	var err error
	if m.ids, err = module.Resolve[guid.Source](services); err != nil {
		return err
	}
	if m.idempotency, err = module.Resolve[idempotency.Store](services); err != nil {
		return err
	}
	if m.roles, err = module.Resolve[governance.SeededRoles](services); err != nil {
		return err
	}
	if m.createAuthority, err = module.Resolve[governance.ProjectCreationAuthority](services); err != nil {
		return err
	}
	if m.sources, err = module.Resolve[intake.IntakeReader](services); err != nil {
		return err
	}
	if m.agents, err = module.Resolve[fleet.AgentDirectory](services); err != nil {
		return err
	}
	if m.projectAccess, err = module.Resolve[core.AccessTableNames](services); err != nil {
		return err
	}
	if m.listConstraint, err = module.Resolve[core.TenantConstraint](services); err != nil {
		return err
	}
	if m.entityOwner, err = module.Resolve[core.EntityOwnerResolver](services); err != nil {
		return err
	}
	if m.members, err = module.Resolve[core.ActiveMemberChecker](services); err != nil {
		return err
	}
	if m.principals, err = module.Resolve[identity.PrincipalStateStore](services); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](services); err != nil {
		return err
	}
	if m.registrar == nil || m.registrar.Registry() == nil {
		return ErrUnavailable
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "project", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read projects"},
		{Resource: "project", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "create and manage projects"},
		{Resource: "project", Verb: "stage_change", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "change project stage"},
		{Resource: "project", Verb: "mode_change", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "change project team mode"},
	}, nil); err != nil {
		return fmt.Errorf("register project permissions: %w", err)
	}
	if m.checker, err = module.Resolve[core.AuthorizationEvaluator](services); err != nil {
		return err
	}
	if m.service.audit, err = module.Resolve[audit.Appender](services); err != nil {
		return err
	}
	if m.service.events, err = module.Resolve[events.Publisher](services); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](services)
	if err != nil {
		return err
	}
	return catalog.AddTypes(Name, events.TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal})
}
func (m *Module) Initialize(ctx context.Context) error {
	if m == nil || m.service == nil || m.ids == nil || m.projectAccess.Entities.IsZero() || m.listConstraint == nil || m.entityOwner == nil || m.idempotency == nil || m.createAuthority == nil || m.principals.Lock == nil || m.checker == nil || m.service.audit == nil || m.service.events == nil || m.roles.EnsureTenant == nil || m.roles.RoleIDOn == nil || m.members == nil || m.principals.ActiveHumanByID == nil || m.agents.ByPrincipalOn == nil || m.sources.GetOn == nil || m.sources.ContactsOn == nil {
		return ErrUnavailable
	}
	if err := deliveryi18n.Register(); err != nil {
		return err
	}
	for name, missing := range map[string]bool{
		"core.EntityProvisioner":      m.service.entities.CreateOn == nil || m.service.entities.GetOn == nil,
		"core.EntityRoleBindingStore": m.service.roles.PutOn == nil,
		"collab.ChannelProvisioner":   m.service.channels.CreateDefaultOn == nil,
	} {
		if missing {
			slog.WarnContext(ctx, "project creation unavailable; enable and wire provisioning provider", "capability", name)
		}
	}
	m.initialized = true
	return nil
}

var (
	_ module.HTTPRegistrar           = (*Module)(nil)
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
