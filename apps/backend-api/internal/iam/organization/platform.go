package organization

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"context"
)

type Platform = Tenant
type CreatePlatform = CreateTenant
type UpdatePlatform = UpdateTenant

// PlatformService shares organization root workflows with tenants while fixing
// the immutable owner kind at construction.
type PlatformService struct {
	*TenantService
}

func NewPlatformService(runtime *appenv.Runtime, appender audit.Appender, ids guid.Source, bootstrap core.TenantBootstrapper, owners identity.TenantOwnerLookup) *PlatformService {
	service := NewTenantService(runtime, appender, ids, bootstrap, owners)
	service.scopeKind = ScopePlatform
	return &PlatformService{TenantService: service}
}

func (r *Repository) rootKind(ctx context.Context, id guid.ID) (string, error) {
	tables, _, err := r.tables()
	if err != nil {
		return "", err
	}
	var row struct{ ScopeKind string }
	if err := r.writer().WithContext(ctx).Table(tables.Tenants.TableName()).Select("scope_kind").Where("id = ?", id).Take(&row).Error; err != nil {
		return "", err
	}
	return row.ScopeKind, nil
}

func sameDepartmentEntity(kind string, current, parent *guid.ID) bool {
	if kind == ScopePlatform {
		return current == nil && parent == nil
	}
	return kind == ScopeTenant && current != nil && parent != nil && *current == *parent
}
