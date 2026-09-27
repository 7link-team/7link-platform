package organization

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/iam/organization/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ScopeTenant   = "tenant"
	ScopePlatform = "platform"
	ScopeSystem   = "system"

	TenantActive    = "active"
	TenantSuspended = "suspended"
	TenantDeleted   = "deleted"

	// tenantOwnerActiveStatus is the principal status identity reports for an
	// account that may sign in. Only such a principal may be handed a tenant.
	tenantOwnerActiveStatus = "active"
)

var (
	ErrTenantNotFound        = errors.New("tenant not found")
	ErrTenantSlugConflict    = errors.New("tenant slug already exists")
	ErrTenantVersionConflict = errors.New("tenant version conflict")
	ErrInvalidTenant         = errors.New("invalid tenant")
	// ErrTenantOwnerNotFound and ErrTenantOwnerInactive keep tenant creation
	// create-or-nothing: a tenant whose named owner cannot administer it would
	// commit as a shell no HTTP call could ever reach.
	ErrTenantOwnerNotFound = errors.New("tenant owner principal not found")
	ErrTenantOwnerInactive = errors.New("tenant owner principal is not active")
	tenantSlugPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`)
)

type Tenant struct {
	ID        guid.ID   `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type CreateTenant struct {
	Slug string
	Name string
	// OwnerPrincipalID is the principal that becomes the tenant's first
	// administrator. It is required: every tenant route demands an active
	// membership, so a tenant created without an owner is unreachable.
	OwnerPrincipalID guid.ID
}
type UpdateTenant struct {
	Name    *string
	Status  *string
	Version int64
}

type TenantService struct {
	scopeKind string
	repo      *Repository
	now       func() time.Time
	bootstrap core.TenantBootstrapper
	roots     core.FileRootProvisioner
	owners    identity.TenantOwnerLookup
}

func (s *TenantService) kind() string {
	if s.scopeKind == "" {
		return ScopeTenant
	}
	return s.scopeKind
}

// NewTenantService builds the tenant service. The bootstrapper and owner
// lookup are required rather than optional: without them Create could only
// produce tenants nobody can administer, which is the defect this service
// exists to prevent.
func NewTenantService(
	runtime *appenv.Runtime,
	appender audit.Appender,
	ids guid.Source,
	bootstrap core.TenantBootstrapper,
	owners identity.TenantOwnerLookup,
) *TenantService {
	if runtime == nil || appender == nil || ids == nil {
		panic("tenant service requires runtime, audit appender, and id generator")
	}
	if bootstrap.Bootstrap == nil || bootstrap.IsUnclaimed == nil || owners == nil {
		panic("tenant service requires the tenant bootstrapper and owner lookup")
	}
	return &TenantService{
		repo: NewRepository(runtime, ids, appender), now: time.Now,
		bootstrap: bootstrap, owners: owners,
	}
}

// EnsureTenant idempotently creates the configured bootstrap tenant.
func EnsureTenant(ctx context.Context, runtime *appenv.Runtime, id guid.ID) error {
	if runtime == nil || runtime.Database() == nil || runtime.Database().Write() == nil {
		return fmt.Errorf("ensure tenant: database is required")
	}
	if id.Zero() {
		return ErrInvalidTenant
	}
	if err := goosex.ValidatePrefix(runtime.TablePrefix()); err != nil {
		return fmt.Errorf("ensure tenant: %w", err)
	}
	now := organizationTime(time.Now())
	row := model.Tenant{ID: id, ScopeKind: ScopeTenant, Slug: tenantSlugForID(id.String()), Name: id.String(), Status: TenantActive, Version: 1, CreatedAt: now, UpdatedAt: now}
	database := runtime.Database().Write().WithContext(ctx).Table(query.BindNames(runtime.TablePrefix()).Tenants.String())
	err := database.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("ensure tenant: %w", err)
	}
	var count int64
	if err := database.Where("id = ? AND scope_kind = ?", id, ScopeTenant).Count(&count).Error; err != nil {
		return fmt.Errorf("ensure tenant: %w", err)
	}
	if count != 1 {
		return ErrTenantNotFound
	}
	return nil
}

func (s *TenantService) List(ctx context.Context, includeDeleted bool) ([]Tenant, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	tables, _, err := repo.tables()
	if err != nil {
		return nil, err
	}
	var rows []model.Tenant
	db := repo.writer().WithContext(ctx).Table(tables.Tenants.TableName()).Where("scope_kind = ?", s.kind())
	if !includeDeleted {
		db = db.Where("status <> ?", TenantDeleted)
	}
	if err := db.Order("name ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	items := make([]Tenant, len(rows))
	for index := range rows {
		items[index] = tenantFromRow(&rows[index])
	}
	return items, nil
}

func (s *TenantService) ListByMember(ctx context.Context, subject guid.ID) ([]Tenant, error) {
	if subject.Zero() {
		return nil, ErrInvalidTenant
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	memberTable := repo.foreign.access.TenantMembers.String()
	tables, _, err := repo.tables()
	if err != nil {
		return nil, err
	}
	var rows []model.Tenant
	tenantTable := tables.Tenants.TableName()
	err = repo.writer().WithContext(ctx).Table(tenantTable+" AS t").Select("t.*").
		Joins("JOIN "+memberTable+" AS m ON m.tenant_id = t.id AND m.principal_id = ?", subject).
		Where("t.scope_kind = ? AND t.status <> ? AND m.status = ?", s.kind(), TenantDeleted, StatusActive).Order("t.name ASC, t.id ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list member tenants: %w", err)
	}
	items := make([]Tenant, len(rows))
	for index := range rows {
		items[index] = tenantFromRow(&rows[index])
	}
	return items, nil
}

func (s *TenantService) Get(ctx context.Context, id guid.ID) (Tenant, error) {
	if id.Zero() {
		return Tenant{}, ErrInvalidTenant
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return Tenant{}, err
	}
	row, err := getRootRow(ctx, repo, id, s.kind())
	if err != nil {
		return Tenant{}, err
	}
	return tenantFromRow(row), nil
}

func (s *TenantService) Create(ctx context.Context, input CreateTenant) (Tenant, error) {
	input, err := normalizeTenantCreate(input)
	if err != nil {
		return Tenant{}, err
	}
	id, err := s.repo.nextID()
	if err != nil {
		return Tenant{}, fmt.Errorf("generate tenant id: %w", err)
	}
	now := organizationTime(s.now())
	row := model.Tenant{ID: id, ScopeKind: s.kind(), Slug: input.Slug, Name: input.Name, Status: TenantActive, Version: 1, CreatedAt: now, UpdatedAt: now}
	event := audit.NewDomainEvent(ctx, 0, s.kind()+"_created", s.kind(), id)
	event.Detail["slug"] = input.Slug
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		database := repo.writer().WithContext(ctx)
		err = database.Table(tables.Tenants.TableName()).Create(&row).Error
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrTenantSlugConflict
		}
		if err != nil {
			return fmt.Errorf("insert tenant: %w", err)
		}
		// The owner is resolved inside the transaction that inserts the
		// tenant, so a principal suspended between request and commit cannot
		// still end up administering it.
		owner, found, err := s.owners(ctx, repo.writer(), input.OwnerPrincipalID)
		if err != nil {
			return fmt.Errorf("resolve tenant owner: %w", err)
		}
		if !found {
			return ErrTenantOwnerNotFound
		}
		if owner.Status != tenantOwnerActiveStatus {
			return ErrTenantOwnerInactive
		}
		result, err := s.bootstrap.Bootstrap(ctx, repo.writer(), core.TenantBootstrapInput{
			TenantID: id, OwnerID: owner.ID, ScopeKind: s.kind(),
			DisplayName: owner.DisplayName, Email: owner.Email,
		}, now)
		if err != nil {
			return fmt.Errorf("bootstrap tenant: %w", err)
		}
		if s.roots != nil && s.kind() == ScopeTenant {
			if _, err := s.roots(ctx, repo.writer(), core.FileRootProvisionInput{
				Scope: "tenant", TenantID: id, OwnerID: id,
			}, now); err != nil {
				return fmt.Errorf("provision tenant file root: %w", err)
			}
		}
		if err := repo.bumpPolicyRevision(ctx, id, now); err != nil {
			return err
		}
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		bootstrapped := audit.NewDomainEvent(ctx, id, s.kind()+"_bootstrapped", s.kind(), id)
		bootstrapped.Detail["owner_principal_id"] = owner.ID.String()
		bootstrapped.Detail["administrator_role_id"] = result.AdministratorRoleID.String()
		bootstrapped.Detail["permission_count"] = result.PermissionCount
		bootstrapped.Detail["menu_count"] = result.MenuCount
		return repo.appendAudit(ctx, bootstrapped)
	})
	if err != nil {
		return Tenant{}, fmt.Errorf("create tenant: %w", err)
	}
	return tenantFromRow(&row), nil
}

func (s *TenantService) Update(ctx context.Context, id guid.ID, input UpdateTenant) (Tenant, error) {
	id, input, err := normalizeTenantUpdate(id, input)
	if err != nil {
		return Tenant{}, err
	}
	now := organizationTime(s.now())
	var updated *model.Tenant
	event := audit.NewDomainEvent(ctx, 0, s.kind()+"_updated", s.kind(), id)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		current, err := getRootRow(ctx, repo, id, s.kind())
		if err != nil {
			return err
		}
		if current.Status == TenantDeleted || current.Version != input.Version {
			return ErrTenantVersionConflict
		}
		candidate := *current
		updated = &candidate
		if input.Name != nil {
			updated.Name = *input.Name
		}
		if input.Status != nil {
			updated.Status = *input.Status
		}
		if updated.Name == current.Name && updated.Status == current.Status {
			return nil
		}
		updated.Version, updated.UpdatedAt = current.Version+1, now
		result := repo.writer().WithContext(ctx).Table(tables.Tenants.TableName()).Where("id = ? AND scope_kind = ? AND version = ?", id, s.kind(), current.Version).
			Select("name", "status", "version", "updated_at").Updates(updated)
		if result.Error != nil {
			return fmt.Errorf("update tenant: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrTenantVersionConflict
		}
		if err := repo.bumpPolicyRevision(ctx, id, now); err != nil {
			return err
		}
		event.Detail["status"] = updated.Status
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Tenant{}, fmt.Errorf("update tenant: %w", err)
	}
	return tenantFromRow(updated), nil
}

func (s *TenantService) Delete(ctx context.Context, id guid.ID, version int64) error {
	if id.Zero() || version < 1 {
		return ErrInvalidTenant
	}
	now := organizationTime(s.now())
	event := audit.NewDomainEvent(ctx, 0, s.kind()+"_deleted", s.kind(), id)
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		current, err := getRootRow(ctx, repo, id, s.kind())
		if err != nil {
			return err
		}
		if current.Status == TenantDeleted || current.Version != version {
			return ErrTenantVersionConflict
		}
		result := repo.writer().WithContext(ctx).Table(tables.Tenants.TableName()).Where("id = ? AND scope_kind = ? AND version = ?", id, s.kind(), version).
			Updates(map[string]any{"status": TenantDeleted, "version": gorm.Expr("version + 1"), "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("delete tenant: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrTenantVersionConflict
		}
		if err := repo.bumpPolicyRevision(ctx, id, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	return nil
}

func getTenantRow(ctx context.Context, repo *Repository, id guid.ID) (*model.Tenant, error) {
	return getRootRow(ctx, repo, id, ScopeTenant)
}

func getRootRow(ctx context.Context, repo *Repository, id guid.ID, kind string) (*model.Tenant, error) {
	if repo == nil || id.Zero() {
		return nil, ErrInvalidTenant
	}
	tables, _, err := repo.tables()
	if err != nil {
		return nil, err
	}
	var row model.Tenant
	err = repo.writer().WithContext(ctx).Table(tables.Tenants.TableName()).Where("id = ? AND scope_kind = ?", id, kind).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	return &row, nil
}

func tenantFromRow(row *model.Tenant) Tenant {
	return Tenant{ID: row.ID, Slug: row.Slug, Name: row.Name, Status: row.Status, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func normalizeTenantCreate(input CreateTenant) (CreateTenant, error) {
	input.Slug, input.Name = strings.ToLower(strings.TrimSpace(input.Slug)), strings.TrimSpace(input.Name)
	if !validTenantSlug(input.Slug) || !validTenantName(input.Name) {
		return CreateTenant{}, ErrInvalidTenant
	}
	if input.OwnerPrincipalID.Zero() {
		return CreateTenant{}, ErrInvalidTenant
	}
	return input, nil
}

func normalizeTenantUpdate(id guid.ID, input UpdateTenant) (guid.ID, UpdateTenant, error) {
	if id.Zero() || input.Version < 1 || (input.Name == nil && input.Status == nil) {
		return 0, UpdateTenant{}, ErrInvalidTenant
	}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		if !validTenantName(value) {
			return 0, UpdateTenant{}, ErrInvalidTenant
		}
		input.Name = &value
	}
	if input.Status != nil && *input.Status != TenantActive && *input.Status != TenantSuspended {
		return 0, UpdateTenant{}, ErrInvalidTenant
	}
	return id, input, nil
}

func validTenantSlug(value string) bool { return tenantSlugPattern.MatchString(value) }
func validTenantName(value string) bool { return validName(value) }

func tenantSlugForID(id string) string {
	lower := strings.ToLower(id)
	if validTenantSlug(lower) {
		return lower
	}
	if len(lower) < 3 && strings.IndexFunc(lower, func(r rune) bool { return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) }) < 0 {
		return "tenant-" + lower
	}
	var slug strings.Builder
	for _, r := range lower {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			slug.WriteRune(r)
		} else if slug.Len() > 0 && !strings.HasSuffix(slug.String(), "-") {
			slug.WriteByte('-')
		}
	}
	base := strings.Trim(slug.String(), "-")
	if base == "" {
		base = "tenant"
	}
	suffix := fmt.Sprintf("-%x", sha256.Sum256([]byte(id)))[:9]
	if len(base) > 63-len(suffix) {
		base = strings.TrimRight(base[:63-len(suffix)], "-")
	}
	return base + suffix
}
