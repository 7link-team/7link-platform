package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/passwordx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalid           = errors.New("invalid principal")
	ErrNotFound          = errors.New("principal not found")
	ErrLoginConflict     = errors.New("login name already exists")
	ErrPrincipalInactive = errors.New("principal or tenant membership is not active")
	ErrProfileDenied     = errors.New("global profile update requires principal or platform authority")
	// ErrPrincipalSharedBeyondTenant refuses a tenant-route suspension of an
	// account that another organization or the platform also relies on.
	ErrPrincipalSharedBeyondTenant = errors.New("principal is shared beyond this tenant; remove the membership instead")
)

type tenantMemberRecord struct {
	TenantID    guid.ID    `gorm:"column:tenant_id"`
	PrincipalID guid.ID    `gorm:"column:principal_id"`
	DisplayName string     `gorm:"column:display_name"`
	Email       string     `gorm:"column:email"`
	Status      string     `gorm:"column:status"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
	DisabledAt  *time.Time `gorm:"column:disabled_at"`
}

// Service coordinates principal and service-account identity workflows.
type Service struct {
	repo                *Repository
	administrators      core.AdministratorGuard
	fields              authz.FieldPermissionChecker
	avatarPermissions   authz.PermissionChecker
	permissionRegistry  *authz.Registry
	anonymizer          PrincipalAnonymizer
	erasureRecorder     audit.ErasureRecorder
	privacyBrowser      authnext.BrowserSessionSecurity
	reauthenticate      authnext.ReauthenticationConsumer
	closeCredentials    PrincipalCredentialsRevoker
	closeOAuth          PrincipalOAuthRevoker
	platformAccessGuard core.PrincipalPlatformAccessGuard
	revokeTenantTokens  core.TenantPersonalTokenRevoker
	revokeTenantOAuth   core.TenantOAuthRevoker
	now                 func() time.Time
}

func NewService(
	runtime *appenv.Runtime,
	appender audit.Appender,
	administrators core.AdministratorGuard,
	ids guid.Source,
	permissions *authz.Registry,
) *Service {
	if runtime == nil || appender == nil || administrators == nil || ids == nil || permissions == nil {
		panic("identity service requires runtime, audit appender, administrator guard, id generator, and permission registry")
	}
	return &Service{
		repo: NewRepository(runtime, ids, appender), administrators: administrators, permissionRegistry: permissions, now: time.Now,
	}
}

func (s *Service) resolveServiceAccountOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	tenantID, found, err := s.repo.serviceAccountOwner(ctx, id)
	return authz.ResourceOwner{TenantID: tenantID}, found, err
}

func (s *Service) Create(
	ctx context.Context,
	tenantID guid.ID,
	loginName, password, displayName, email string,
) (Principal, error) {
	if tenantID.Zero() {
		return Principal{}, ErrInvalid
	}
	return s.createPrincipal(ctx, tenantID, loginName, password, displayName, email)
}

func (s *Service) CreateGlobal(ctx context.Context, loginName, password, displayName, email string) (Principal, error) {
	return s.createPrincipal(ctx, 0, loginName, password, displayName, email)
}

func (s *Service) createPrincipal(ctx context.Context, tenantID guid.ID, loginName, password, displayName, email string) (Principal, error) {
	loginName = normalizeLogin(loginName)
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if loginName == "" || len(loginName) > 200 || len(password) < 12 ||
		len(password) > 1024 || len(displayName) > 128 || len(email) > 320 {
		return Principal{}, ErrInvalid
	}
	if displayName == "" {
		displayName = loginName
	}
	passwordHash, err := passwordx.Hash(password)
	if err != nil {
		return Principal{}, err
	}
	id, err := s.repo.nextID()
	if err != nil {
		return Principal{}, err
	}
	now := identityTime(s.now())
	row := model.Principal{
		ID: id, LoginName: loginName, Email: email, DisplayName: displayName,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	event := audit.NewDomainEvent(ctx, tenantID, "principal_created", "principal", id)
	event.Detail["membership_created"] = !tenantID.Zero()
	event.Detail["status"] = "active"
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.insertPrincipal(ctx, &row); err != nil {
			return err
		}
		if err := repo.createCredential(ctx, id, passwordHash, now); err != nil {
			return err
		}
		if !tenantID.Zero() {
			if err := repo.insertTenantMember(ctx, tenantMemberRecord{
				TenantID: tenantID, PrincipalID: id, DisplayName: displayName, Email: email,
				Status: "active", CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
			if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
				return err
			}
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Principal{}, fmt.Errorf("create principal: %w", err)
	}
	return principalFromRow(&row), nil
}

// CreateInvitedPrincipal writes the identity, credential, and membership
// through the organization's caller-owned transaction.
func (s *Service) CreateInvitedPrincipal(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
	loginName, password, displayName, email string,
) (guid.ID, error) {
	loginName = normalizeLogin(loginName)
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	parsed, emailErr := mail.ParseAddress(email)
	if db == nil || tenantID.Zero() || loginName == "" || len(loginName) > 200 ||
		len(password) < 12 || len(password) > 1024 || len(displayName) > 128 || len(email) > 320 ||
		emailErr != nil || !strings.EqualFold(parsed.Address, email) {
		return 0, ErrInvalid
	}
	if displayName == "" {
		displayName = loginName
	}
	passwordHash, err := passwordx.Hash(password)
	if err != nil {
		return 0, err
	}
	repo, err := s.repo.on(db)
	if err != nil {
		return 0, err
	}
	id, err := repo.nextID()
	if err != nil {
		return 0, err
	}
	now := identityTime(s.now())
	principal := model.Principal{
		ID: id, LoginName: loginName, Email: email, EmailVerified: true,
		DisplayName: displayName, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.insertPrincipal(ctx, &principal); err != nil {
		return 0, fmt.Errorf("insert invited principal: %w", err)
	}
	if err := repo.createCredential(ctx, id, passwordHash, now); err != nil {
		return 0, fmt.Errorf("insert invited credential: %w", err)
	}
	if err := repo.insertTenantMember(ctx, tenantMemberRecord{
		TenantID: tenantID, PrincipalID: id, DisplayName: displayName, Email: email,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return 0, fmt.Errorf("insert invited membership: %w", err)
	}
	return id, nil
}

// CreatePendingPrincipal writes a self-registration identity and pre-hashed
// credential through the authentication workflow's transaction. It grants no
// tenant membership.
func (s *Service) CreatePendingPrincipal(
	ctx context.Context,
	db *gorm.DB,
	email, passwordHash, displayName string,
	now time.Time,
	ids guid.Source,
) (guid.ID, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	parsed, err := mail.ParseAddress(email)
	if s == nil || s.repo == nil || db == nil || len(email) > 320 || passwordHash == "" ||
		len(displayName) > 128 || err != nil || !strings.EqualFold(parsed.Address, email) || ids == nil {
		return 0, ErrInvalid
	}
	if displayName == "" {
		displayName = email
	}
	id, err := nextIdentityID(ids)
	if err != nil {
		return 0, err
	}
	now = identityTime(now)
	repo, err := s.repo.on(db)
	if err != nil {
		return 0, err
	}
	principal := model.Principal{
		ID: id, LoginName: email, Email: email, ActivationRequired: true, SelfRegistered: true,
		DisplayName: displayName, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.insertPrincipal(ctx, &principal); err != nil {
		return 0, fmt.Errorf("insert pending principal: %w", err)
	}
	if err := repo.createCredential(ctx, id, passwordHash, now); err != nil {
		return 0, fmt.Errorf("insert pending credential: %w", err)
	}
	return id, nil
}

// EnsureExternalPrincipal creates a new external principal and membership.
// An asserted email never proves ownership of an existing global account.
func (s *Service) EnsureExternalPrincipal(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
	email, displayName string,
	now time.Time,
) (guid.ID, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	parsed, err := mail.ParseAddress(email)
	if s == nil || s.repo == nil || db == nil || tenantID.Zero() || err != nil ||
		!strings.EqualFold(parsed.Address, email) || len(email) > 320 || len(displayName) > 128 {
		return 0, false, ErrInvalid
	}
	if displayName == "" {
		displayName = email
	}
	now = identityTime(now)
	repo, err := s.repo.on(db)
	if err != nil {
		return 0, false, err
	}
	row, err := repo.principalByLoginOrEmail(ctx, email)
	if err != nil {
		return 0, false, err
	}
	if row != nil {
		return 0, false, ErrLoginConflict
	}
	id, err := repo.nextID()
	if err != nil {
		return 0, false, err
	}
	principal := model.Principal{
		ID: id, LoginName: email, Email: email, EmailVerified: true,
		DisplayName: displayName, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	inserted, err := repo.insertExternalPrincipal(ctx, &principal)
	if err != nil {
		return 0, false, err
	}
	if !inserted {
		return 0, false, ErrLoginConflict
	}
	if _, err := repo.ensureTenantMember(ctx, tenantID, &principal, now); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (s *Service) List(
	ctx context.Context,
	tenantID guid.ID,
	search string,
	limit, offset int,
) ([]Principal, int64, error) {
	if tenantID.Zero() || limit < 1 || limit > 200 || offset < 0 {
		return nil, 0, ErrInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listPrincipals(ctx, tenantID, search, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items := make([]Principal, len(rows))
	for index, row := range rows {
		items[index] = principalFromRow(row)
	}
	return items, total, nil
}

func (s *Service) Get(ctx context.Context, tenantID, id guid.ID) (Principal, error) {
	if tenantID.Zero() || id.Zero() {
		return Principal{}, ErrInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return Principal{}, err
	}
	row, err := repo.getPrincipal(ctx, tenantID, id)
	if err != nil {
		return Principal{}, err
	}
	return principalFromRow(row), nil
}

func (s *Service) Update(
	ctx context.Context,
	tenantID, id guid.ID,
	displayName, email *string,
) (Principal, error) {
	if tenantID.Zero() {
		return Principal{}, ErrInvalid
	}
	return s.updateProfile(ctx, tenantID, id, displayName, email)
}

func (s *Service) UpdateGlobal(ctx context.Context, id guid.ID, displayName, email *string) (Principal, error) {
	return s.updateProfile(ctx, 0, id, displayName, email)
}

func (s *Service) updateProfile(ctx context.Context, tenantID, id guid.ID, displayName, email *string) (Principal, error) {
	if id.Zero() || displayName == nil && email == nil {
		return Principal{}, ErrInvalid
	}
	var requestedDisplay, requestedEmail *string
	if displayName != nil {
		value := strings.TrimSpace(*displayName)
		if value == "" || len(value) > 128 {
			return Principal{}, ErrInvalid
		}
		requestedDisplay = &value
	}
	if email != nil {
		value := strings.ToLower(strings.TrimSpace(*email))
		if len(value) > 320 {
			return Principal{}, ErrInvalid
		}
		requestedEmail = &value
	}
	actor := authnext.PrincipalIDFromContext(ctx)
	if actor.Zero() {
		return Principal{}, ErrProfileDenied
	}
	if actor != id {
		if s.avatarPermissions == nil {
			return Principal{}, ErrUnavailable
		}
		permission := "platform_administer"
		if tenantID.Zero() {
			permission = "system_account_update"
		}
		allowed, err := s.avatarPermissions.CheckPlatform(ctx, permission, actor)
		if err != nil {
			return Principal{}, err
		}
		if !allowed {
			return Principal{}, ErrProfileDenied
		}
	}
	now := identityTime(s.now())
	event := audit.NewDomainEvent(ctx, tenantID, "principal_updated", "principal", id)
	var updated Principal
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getPrincipal(ctx, tenantID, id)
		if err != nil {
			return err
		}
		wantedDisplay, wantedEmail := current.DisplayName, current.Email
		if requestedDisplay != nil {
			wantedDisplay = *requestedDisplay
		}
		if requestedEmail != nil {
			wantedEmail = *requestedEmail
		}
		displayChanged := wantedDisplay != current.DisplayName
		emailChanged := wantedEmail != current.Email
		updates := map[string]any{"display_name": wantedDisplay, "email": wantedEmail, "updated_at": now}
		if emailChanged {
			updates["email_verified"] = false
		}
		if err := repo.updatePrincipalRow(ctx, id, updates); err != nil {
			return err
		}
		if emailChanged {
			if err := repo.incrementCredentialVersion(ctx, id, now); err != nil {
				return err
			}
			if err := repo.revokeAllSecurityState(ctx, id, now); err != nil {
				return err
			}
		}
		event.Detail["display_name_changed"] = displayChanged
		event.Detail["email_changed"] = emailChanged
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		current.DisplayName, current.Email, current.UpdatedAt = wantedDisplay, wantedEmail, now
		if emailChanged {
			current.EmailVerified = false
		}
		updated = principalFromRow(current)
		return nil
	})
	if err != nil {
		return Principal{}, fmt.Errorf("update principal: %w", err)
	}
	return updated, nil
}

func (s *Service) SetStatus(ctx context.Context, tenantID, id guid.ID, status string) (Principal, error) {
	if tenantID.Zero() || id.Zero() || status != "active" && status != "disabled" {
		return Principal{}, ErrInvalid
	}
	now := identityTime(s.now())
	eventType := "principal_restored"
	if status == "disabled" {
		eventType = "principal_disabled"
	}
	event := audit.NewDomainEvent(ctx, tenantID, eventType, "principal", id)
	var updated Principal
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getPrincipal(ctx, tenantID, id)
		if err != nil {
			return err
		}
		verifiers := make([]func() error, 0)
		if current.Status != status {
			if _, err := repo.lifecyclePrincipal(ctx, id, true); err != nil {
				return err
			}
			tenantIDs, err := repo.activeTenantIDs(ctx, id)
			if err != nil {
				return err
			}
			if err := s.requireTenantOnlyPrincipal(ctx, repo, tenantID, id, tenantIDs); err != nil {
				return err
			}
			for _, affectedTenantID := range tenantIDs {
				if status != "disabled" {
					break
				}
				verify, err := s.administrators.Protect(ctx, repo.writer(), affectedTenantID)
				if err != nil {
					return err
				}
				verifiers = append(verifiers, verify)
			}
		}
		var disabledAt *time.Time
		if status == "disabled" {
			disabledAt = &now
		}
		if err := repo.updatePrincipalRow(ctx, id, map[string]any{
			"status": status, "disabled_at": disabledAt, "updated_at": now,
		}); err != nil {
			return err
		}
		if status == "disabled" {
			if err := repo.revokeSessionsAndRefreshTokens(ctx, id, now); err != nil {
				return err
			}
		}
		for _, verify := range verifiers {
			if err := verify(); err != nil {
				return err
			}
		}
		event.Detail["changed"] = current.Status != status
		event.Detail["status"] = status
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		current.Status, current.DisabledAt, current.UpdatedAt = status, disabledAt, now
		updated = principalFromRow(current)
		return nil
	})
	if err != nil {
		return Principal{}, fmt.Errorf("set principal status: %w", err)
	}
	return updated, nil
}

// requireTenantOnlyPrincipal keeps one tenant from suspending or restoring a
// global account that another organization root or the platform also relies
// on: suspension locks the principal out everywhere and restoring reactivates
// it everywhere, so that tenant removes its membership instead. The caller
// holds the principal lock.
func (s *Service) requireTenantOnlyPrincipal(ctx context.Context, repo *Repository, tenantID, id guid.ID, activeTenantIDs []guid.ID) error {
	for _, other := range activeTenantIDs {
		if other != tenantID {
			return ErrPrincipalSharedBeyondTenant
		}
	}
	if s.platformAccessGuard == nil {
		return ErrUnavailable
	}
	if err := s.platformAccessGuard(ctx, repo.writer(), id); err != nil {
		if errors.Is(err, core.ErrPrincipalHasPlatformAccess) {
			return fmt.Errorf("%w: %w", ErrPrincipalSharedBeyondTenant, err)
		}
		return err
	}
	return nil
}

func (r *Repository) insertPrincipal(ctx context.Context, row *model.Principal) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.Principals.TableName()).Create(row)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return ErrLoginConflict
	}
	return result.Error
}

func (r *Repository) insertExternalPrincipal(ctx context.Context, row *model.Principal) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(tables.Principals.TableName()).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "login_name"}}, DoNothing: true,
	}).Create(row)
	if result.Error != nil {
		return false, fmt.Errorf("insert external principal: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func (r *Repository) insertTenantMember(ctx context.Context, member tenantMemberRecord) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	members := r.foreign.access.TenantMembers.String()
	if err := db.WithContext(ctx).Table(members).Create(&member).Error; err != nil {
		return fmt.Errorf("insert tenant membership: %w", err)
	}
	return nil
}

func (r *Repository) getPrincipal(ctx context.Context, tenantID, id guid.ID) (*model.Principal, error) {
	if tenantID.Zero() {
		return r.lifecyclePrincipal(ctx, id, r.inTx)
	}
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	members := r.foreign.access.TenantMembers.String()
	var row model.Principal
	err = db.WithContext(ctx).Table(tables.Principals.TableName()+" AS principal").
		Joins("JOIN "+members+" AS member ON member.principal_id = principal.id").
		Where("member.tenant_id = ? AND principal.id = ?", tenantID, id).
		Where("NOT EXISTS (?)", db.Table(tables.ServiceAccounts.TableName()+" AS service_account").
			Select("1").Where("service_account.principal_id = principal.id")).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get principal: %w", err)
	}
	return &row, nil
}

func (r *Repository) listPrincipals(
	ctx context.Context,
	tenantID guid.ID,
	search string,
	limit, offset int,
) ([]*model.Principal, int64, error) {
	return r.listPrincipalsFiltered(ctx, tenantID, search, limit, offset, "", 0, "")
}

func (r *Repository) listPrincipalsFiltered(ctx context.Context, tenantID guid.ID, search string, limit, offset int, category string, ownerID guid.ID, status string) ([]*model.Principal, int64, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	query := db.WithContext(ctx).Table(tables.Principals.TableName()+" AS principal").
		Where("NOT EXISTS (?)", db.Table(tables.ServiceAccounts.TableName()+" AS service_account").
			Select("1").Where("service_account.principal_id = principal.id"))
	if !tenantID.Zero() {
		members := r.foreign.access.TenantMembers.String()
		query = query.Joins("JOIN "+members+" AS member ON member.principal_id = principal.id").Where("member.tenant_id = ?", tenantID)
	}
	if status != "" {
		query = query.Where("principal.status = ?", status)
	}
	if category == "system" || category == "platform" || category == "tenant" {
		if r.foreign == nil || !r.foreign.directory.Bound() || !r.foreign.access.Bound() {
			return nil, 0, errRepositoryUnavailable
		}
		membership := db.Table(r.foreign.access.TenantMembers.String()+" AS membership").Select("1").
			Joins("JOIN "+r.foreign.directory.Tenants.String()+" AS owner ON owner.id = membership.tenant_id").
			Where("membership.principal_id = principal.id AND owner.scope_kind = ?", category)
		if ownerID > 0 {
			membership = membership.Where("owner.id = ?", ownerID)
		}
		if category == "system" {
			full := db.Table(r.foreign.access.PlatformAdministrators.String() + " AS administrator").Select("1").Where("administrator.principal_id = principal.id")
			query = query.Where("EXISTS (?) OR EXISTS (?)", membership, full)
		} else {
			query = query.Where("EXISTS (?)", membership)
		}
	}
	search = strings.ToLower(strings.TrimSpace(search))
	if search != "" {
		pattern := "%" + search + "%"
		query = query.Where(
			"LOWER(principal.login_name) LIKE ? OR LOWER(principal.display_name) LIKE ? OR LOWER(principal.email) LIKE ?",
			pattern, pattern, pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count principals: %w", err)
	}
	rows := make([]*model.Principal, 0)
	if err := query.Order("principal.login_name ASC, principal.id ASC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list principals: %w", err)
	}
	return rows, total, nil
}

func (r *Repository) updatePrincipalRow(ctx context.Context, id guid.ID, updates map[string]any) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.Principals.TableName()).Where("id = ?", id).Updates(updates)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return ErrLoginConflict
	}
	if result.Error != nil {
		return fmt.Errorf("update principal: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) updateTenantMember(
	ctx context.Context,
	tenantID, principalID guid.ID,
	updates map[string]any,
) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	members := r.foreign.access.TenantMembers.String()
	result := db.WithContext(ctx).Table(members).
		Where("tenant_id = ? AND principal_id = ?", tenantID, principalID).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update tenant membership: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) activeTenantIDs(ctx context.Context, principalID guid.ID) ([]guid.ID, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	members := r.foreign.access.TenantMembers.String()
	tenantIDs := make([]guid.ID, 0)
	if err := db.WithContext(ctx).Table(members).Select("tenant_id").
		Where("principal_id = ? AND status = 'active'", principalID).Order("tenant_id ASC").Scan(&tenantIDs).Error; err != nil {
		return nil, fmt.Errorf("list active principal tenants: %w", err)
	}
	return tenantIDs, nil
}

func (r *Repository) principalByLoginOrEmail(ctx context.Context, email string) (*model.Principal, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	// Each lookup starts from a fresh statement: a chained *gorm.DB carries the
	// previous query's error, so reusing one handle would report the login
	// miss as the email lookup's result.
	humanPrincipals := func() *gorm.DB {
		return db.WithContext(ctx).Table(tables.Principals.TableName()+" AS principal").
			Where("NOT EXISTS (?)", db.Table(tables.ServiceAccounts.TableName()+" AS service_account").
				Select("1").Where("service_account.principal_id = principal.id"))
	}
	var row model.Principal
	err = humanPrincipals().Where("principal.login_name = ?", email).Take(&row).Error
	if err == nil {
		return &row, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("find external principal by login: %w", err)
	}
	rows := make([]*model.Principal, 0, 2)
	err = humanPrincipals().Where("principal.email = ?", email).
		Order("principal.created_at ASC, principal.id ASC").Limit(2).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("find external principal by email: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) > 1 {
		return nil, ErrLoginConflict
	}
	return rows[0], nil
}

func (r *Repository) ensureTenantMember(
	ctx context.Context,
	tenantID guid.ID,
	principal *model.Principal,
	now time.Time,
) (bool, error) {
	_, db, err := r.tables()
	if err != nil {
		return false, err
	}
	members := r.foreign.access.TenantMembers.String()
	member := tenantMemberRecord{
		TenantID: tenantID, PrincipalID: principal.ID, DisplayName: principal.DisplayName,
		Email: principal.Email, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	result := db.WithContext(ctx).Table(members).
		Clauses(clause.OnConflict{DoNothing: true}).Create(&member)
	if result.Error != nil {
		return false, fmt.Errorf("insert external membership: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		if err := r.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return false, err
		}
		return true, nil
	}
	var existing tenantMemberRecord
	if err := db.WithContext(ctx).Table(members).
		Where("tenant_id = ? AND principal_id = ?", tenantID, principal.ID).Take(&existing).Error; err != nil {
		return false, fmt.Errorf("read external membership: %w", err)
	}
	if existing.Status != "active" {
		return false, ErrPrincipalInactive
	}
	return false, nil
}

func principalFromRow(row *model.Principal) Principal {
	return Principal{
		ID: row.ID, LoginName: row.LoginName, Email: row.Email,
		EmailVerified: row.EmailVerified, ActivationRequired: row.ActivationRequired,
		DisplayName: row.DisplayName, Status: row.Status,
		Avatar:    currentAvatar(row.ID, storedAvatar(row.Avatar)),
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
}

func identityTime(value time.Time) time.Time { return value.UTC().Truncate(time.Millisecond) }

func normalizeLogin(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
