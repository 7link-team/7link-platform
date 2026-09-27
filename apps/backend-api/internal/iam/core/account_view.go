package core

import (
	"context"
	"fmt"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const systemScopeID guid.ID = 1

type AccountViewRoot struct {
	ID     guid.ID `json:"id"`
	Slug   string  `json:"slug"`
	Name   string  `json:"name"`
	Status string  `json:"status"`
}

type AccountViewWorkspace struct {
	ScopeKind         string           `json:"scope_kind"`
	ScopeID           guid.ID          `json:"scope_id"`
	Tenant            *AccountViewRoot `json:"tenant,omitempty"`
	Entity            *APIEntity       `json:"entity,omitempty"`
	OperatingPlatform *AccountViewRoot `json:"operating_platform,omitempty"`
}

func registerAccountViewWorkspaceHTTP(api huma.API, svc *Service, registrar *authz.Registrar) {
	authz.RegisterAuthenticated(registrar, api, huma.Operation{
		OperationID: "iam-account-view-workspace", Method: http.MethodGet,
		Path: "/account-views/workspace", Summary: "Get the verified account view workspace", Tags: []string{"account-views"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[AccountViewWorkspace], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil || claims.AccountView == nil {
			return nil, huma.Error403Forbidden(respx.Forbidden)
		}
		workspace, err := svc.accountViewWorkspace(ctx, claims.AccountView.ScopeType, claims.AccountView.ScopeID)
		if err != nil {
			return nil, apiError("account view workspace", err)
		}
		return respx.OK(ctx, workspace), nil
	})
}

// Metadata follows only the verified token scope, never a caller-selected root.
func (s *Service) accountViewWorkspace(ctx context.Context, kind string, id guid.ID) (AccountViewWorkspace, error) {
	out := AccountViewWorkspace{ScopeKind: kind, ScopeID: id}
	rootID := id
	if kind == "personal" || kind == "system" {
		return out, nil
	}
	if kind == "entity" {
		owner, found, err := s.resolveEntityOwner(ctx, id)
		if err != nil {
			return out, err
		}
		if !found {
			return out, gorm.ErrRecordNotFound
		}
		entity, err := s.GetEntity(ctx, owner.TenantID, id)
		if err != nil {
			return out, err
		}
		apiEntity := entityFromDomain(entity)
		out.Entity = &apiEntity
		rootID = owner.TenantID
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return out, err
	}
	tables, err := repo.directoryTables()
	if err != nil {
		return out, err
	}
	root := new(AccountViewRoot)
	rootKind := "tenant"
	if kind == "platform" {
		rootKind = "platform"
	}
	err = repo.writer().WithContext(ctx).Table(tables.Tenants.String()).Select("id, slug, name, status").Where("id = ? AND scope_kind = ? AND status = ?", rootID, rootKind, "active").Take(root).Error
	if err != nil {
		return out, err
	}
	if kind == "platform" {
		out.OperatingPlatform = root
	} else {
		out.Tenant = root
	}
	return out, nil
}

// View admission is evaluated on issuance and every use. Route authorization
// still evaluates the target's permissions independently.
func (m *Module) authorizeAccountView(ctx context.Context, tx *gorm.DB, actor, target guid.ID, scope string, id guid.ID) error {
	if m == nil || m.checker == nil || m.members == nil || m.registrar == nil || m.runtime == nil || tx == nil || actor <= 0 || target <= 0 || id <= 0 {
		return authnext.ErrInvalidCredentials
	}
	checker := *m.checker
	checker.db = tx
	allowed, err := checker.CheckPlatform(ctx, "account_view_create", actor)
	if err != nil {
		return fmt.Errorf("%w: evaluate account view operator: %w", authnext.ErrUnavailable, err)
	}
	if !allowed {
		return authnext.ErrInvalidCredentials
	}
	switch scope {
	case "personal":
		allowed = id == target
	case "system":
		allowed = false
		if id == systemScopeID {
			for _, action := range m.registrar.Registry().All() {
				if action.Scope != "platform" {
					continue
				}
				allowed, err = checker.CheckPlatform(ctx, action.Code, target)
				if err != nil || allowed {
					break
				}
			}
		}
	case "platform":
		allowed, err = m.members.IsOperatingPlatformMemberActiveOn(ctx, tx, id, target)
	case "tenant":
		allowed, err = m.members.IsMemberActiveOn(ctx, tx, id, target)
	case "entity":
		var entity struct{ TenantID guid.ID }
		err = tx.WithContext(ctx).Table(checker.names.Entities.String()).Select("tenant_id").Where("id = ? AND status = ?", id, EntityActive).Take(&entity).Error
		if err == gorm.ErrRecordNotFound {
			err = nil
			allowed = false
		} else if err == nil {
			allowed, err = m.members.IsMemberActiveOn(ctx, tx, entity.TenantID, target)
		}
	default:
		allowed = false
	}
	if err != nil {
		return fmt.Errorf("%w: evaluate account view target: %w", authnext.ErrUnavailable, err)
	}
	if !allowed {
		return authnext.ErrInvalidCredentials
	}
	return nil
}
