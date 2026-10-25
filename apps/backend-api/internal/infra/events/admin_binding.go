package events

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// AdminConnector accepts IAM's current authority after infrastructure activation.
type AdminConnector struct {
	Bind         func(*authz.Registrar, func(context.Context, *gorm.DB, string, guid.ID) (bool, error)) error
	BindAudit    func(AdminAudit) error
	BindSecurity func(authn.ReauthenticationConsumer, idempotency.Store) error
}
type AdminAudit func(context.Context, *gorm.DB, string, guid.ID, string, guid.ID, map[string]any) error
type adminAccess struct {
	registrar   *authz.Registrar
	check       func(context.Context, *gorm.DB, string, guid.ID) (bool, error)
	audit       AdminAudit
	proof       authn.ReauthenticationConsumer
	idempotency idempotency.Store
}

var errAdminForbidden = errors.New("events: administrative permission denied")
var errAdminInvalid = errors.New("events: invalid administrative request")

func (m *Module) provideAdmin(s *module.Services) error {
	return module.Provide[AdminConnector](s, AdminConnector{
		Bind: func(r *authz.Registrar, check func(context.Context, *gorm.DB, string, guid.ID) (bool, error)) error {
			if r == nil || check == nil {
				return ErrUnavailable
			}
			if err := r.Registry().Contribute([]authz.Action{
				{Resource: "system_event", Verb: "view", Scope: authz.ActionScopeSystem, Menu: true, Summary: "view domain events and subscribers"},
				{Resource: "system_event", Verb: "manage", Scope: authz.ActionScopeSystem, Summary: "manage domain event delivery"},
			}, nil); err != nil {
				return err
			}
			m.admin.registrar, m.admin.check = r, check
			return nil
		},
		BindAudit: func(a AdminAudit) error {
			if a == nil {
				return ErrUnavailable
			}
			m.admin.audit = a
			return nil
		},
		BindSecurity: func(p authn.ReauthenticationConsumer, i idempotency.Store) error {
			if p == nil || i == nil {
				return ErrUnavailable
			}
			m.admin.proof, m.admin.idempotency = p, i
			return nil
		},
	})
}
func (m *Module) adminTransaction(ctx context.Context, manage bool, run func(*gorm.DB) error) error {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.admin.check == nil {
		return ErrUnavailable
	}
	db := m.runtime.Database().Write()
	if db == nil {
		return ErrUnavailable
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.adminAuthorizeOn(ctx, tx, manage); err != nil {
			return err
		}
		return run(tx)
	})
}
func (m *Module) adminAuthorizeOn(ctx context.Context, tx *gorm.DB, manage bool) error {
	if m.admin.check == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 {
		return errAdminForbidden
	}
	code := "system_event_view"
	if manage {
		code = "system_event_manage"
	}
	ok, err := m.admin.check(ctx, tx, code, actor)
	if err != nil {
		return err
	}
	if !ok {
		return errAdminForbidden
	}
	return nil
}
func (m *Module) adminAuditOn(ctx context.Context, tx *gorm.DB, action string, tenant guid.ID, resource string, id guid.ID, detail map[string]any) error {
	if m.admin.audit == nil {
		return ErrUnavailable
	}
	return m.admin.audit(ctx, tx, action, tenant, resource, id, detail)
}
