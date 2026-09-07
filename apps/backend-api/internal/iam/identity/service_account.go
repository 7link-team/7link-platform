package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/passwordx"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxServiceAccountCredentials = 20

var (
	ErrServiceAccountNotFound = errors.New("service account not found")
	ErrServiceAccountInvalid  = errors.New("invalid service account")
	ErrServiceAccountConflict = errors.New("service account version conflict")
	ErrCredentialNotFound     = errors.New("service account credential not found")
	ErrCredentialLimit        = errors.New("service account credential limit reached")
)

type ServiceAccount struct {
	ID          guid.ID    `json:"id"`
	LoginName   string     `json:"login_name"`
	DisplayName string     `json:"display_name"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Version     int64      `json:"version"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type ServiceAccountCredential struct {
	ID               guid.ID    `json:"id"`
	ServiceAccountID guid.ID    `json:"service_account_id"`
	Name             string     `json:"name"`
	Scopes           []string   `json:"scopes"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type ServiceAccountCredentialSecret struct {
	Credential ServiceAccountCredential `json:"credential"`
	Secret     string                   `json:"client_secret" doc:"Shown once. Store it in a secret manager."`
}

type serviceAccountView struct {
	model.ServiceAccount `gorm:"embedded"`
	LoginName            string `gorm:"column:login_name"`
	DisplayName          string `gorm:"column:display_name"`
}

// ValidateServiceAccount never treats an ordinary user or foreign tenant member
// as an automation principal. It reads through the supplied transaction.
func (s *Service) ValidateServiceAccount(ctx context.Context, db *gorm.DB, tenantID, principalID guid.ID, now time.Time) (bool, error) {
	if s == nil || s.repo == nil {
		return false, ErrUnavailable
	}
	if db == nil || tenantID <= 0 || principalID <= 0 || now.IsZero() {
		return false, ErrServiceAccountInvalid
	}
	repo, err := s.repo.on(db)
	if err != nil {
		return false, err
	}
	query, err := repo.serviceAccountQuery(ctx)
	if err != nil {
		return false, err
	}
	var count int64
	err = query.
		Joins("JOIN "+repo.table("iam_tenant_members")+" AS member ON member.principal_id = principal.id AND member.tenant_id = service_account.owner_tenant_id").
		Joins("JOIN "+repo.table("iam_tenants")+" AS tenant ON tenant.id = service_account.owner_tenant_id").
		Where("service_account.owner_tenant_id = ? AND service_account.principal_id = ?", tenantID, principalID).
		Where("service_account.status = 'active' AND service_account.deleted_at IS NULL AND (service_account.expires_at IS NULL OR service_account.expires_at > ?)", now.UTC()).
		Where("principal.status = 'active' AND principal.activation_required = ? AND member.status = 'active' AND tenant.status = 'active'", false).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("validate service account: %w", err)
	}
	return count == 1, nil
}

func (s *Service) CreateServiceAccount(
	ctx context.Context,
	tenantID guid.ID,
	loginName, displayName, description string,
	expiresAt *time.Time,
) (ServiceAccount, error) {
	loginName = normalizeLogin(loginName)
	displayName = strings.TrimSpace(displayName)
	description = strings.TrimSpace(description)
	if displayName == "" {
		displayName = loginName
	}
	now := identityTime(s.now())
	expiresAt = identityOptionalTime(expiresAt)
	if !validServiceAccount(tenantID, loginName, displayName, description, "active", expiresAt, now) {
		return ServiceAccount{}, ErrServiceAccountInvalid
	}
	id, err := s.repo.nextID()
	if err != nil {
		return ServiceAccount{}, err
	}
	principal := model.Principal{
		ID: id, LoginName: loginName, DisplayName: displayName, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	account := model.ServiceAccount{
		PrincipalID: id, OwnerTenantID: tenantID, Description: description, Status: "active",
		ExpiresAt: expiresAt, TokenVersion: 1, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	event := audit.NewDomainEvent(ctx, tenantID, "service_account_created", "service_account", id)
	event.Detail["expires_at"] = expiresAt
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		active, err := repo.tenantActive(ctx, tenantID)
		if err != nil {
			return err
		}
		if !active {
			return ErrServiceAccountInvalid
		}
		if err := repo.insertPrincipal(ctx, &principal); err != nil {
			return err
		}
		if err := repo.insertServiceAccount(ctx, &account); err != nil {
			return err
		}
		if err := repo.insertTenantMember(ctx, tenantMemberRecord{
			TenantID: tenantID, PrincipalID: id, DisplayName: displayName,
			Status: "active", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return ServiceAccount{}, fmt.Errorf("create service account: %w", err)
	}
	return serviceAccountFromRow(&serviceAccountView{
		ServiceAccount: account, LoginName: principal.LoginName, DisplayName: principal.DisplayName,
	}), nil
}

func (s *Service) ListServiceAccounts(
	ctx context.Context,
	tenantID guid.ID,
	search string,
	limit, offset int,
) ([]ServiceAccount, int64, error) {
	if tenantID.Zero() || limit < 1 || limit > 200 || offset < 0 {
		return nil, 0, ErrServiceAccountInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listServiceAccounts(ctx, tenantID, search, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items := make([]ServiceAccount, len(rows))
	for index, row := range rows {
		items[index] = serviceAccountFromRow(row)
	}
	return items, total, nil
}

func (s *Service) GetServiceAccount(ctx context.Context, tenantID, id guid.ID) (ServiceAccount, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return ServiceAccount{}, err
	}
	row, err := repo.getServiceAccount(ctx, tenantID, id, false)
	if err != nil {
		return ServiceAccount{}, err
	}
	return serviceAccountFromRow(row), nil
}

func (s *Service) ReplaceServiceAccount(
	ctx context.Context,
	tenantID, id guid.ID,
	displayName, description, status string,
	expiresAt *time.Time,
	version int64,
) (ServiceAccount, error) {
	displayName = strings.TrimSpace(displayName)
	description = strings.TrimSpace(description)
	status = strings.TrimSpace(status)
	now := identityTime(s.now())
	expiresAt = identityOptionalTime(expiresAt)
	if id.Zero() || version < 1 || !validServiceAccount(
		tenantID, "placeholder", displayName, description, status, expiresAt, now,
	) {
		return ServiceAccount{}, ErrServiceAccountInvalid
	}
	var updated ServiceAccount
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getServiceAccount(ctx, tenantID, id, true)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrServiceAccountConflict
		}
		statusChanged := current.Status != status
		expiryChanged := !sameOptionalTime(current.ExpiresAt, expiresAt)
		var verify func() error
		if current.Status == "active" && status == "disabled" {
			verify, err = s.administrators.Protect(ctx, repo.writer(), tenantID)
			if err != nil {
				return err
			}
		}
		if err := repo.replaceServiceAccount(
			ctx, tenantID, id, description, status, expiresAt, version,
			statusChanged || expiryChanged, now,
		); err != nil {
			return err
		}
		principalStatus := "active"
		var disabledAt *time.Time
		if status == "disabled" {
			principalStatus, disabledAt = "disabled", &now
		}
		if err := repo.updatePrincipalRow(ctx, id, map[string]any{
			"display_name": displayName, "status": principalStatus,
			"disabled_at": disabledAt, "updated_at": now,
		}); err != nil {
			return err
		}
		if err := repo.updateTenantMember(ctx, tenantID, id, map[string]any{
			"display_name": displayName, "updated_at": now,
		}); err != nil {
			return err
		}
		if verify != nil {
			if err := verify(); err != nil {
				return err
			}
		}
		if statusChanged {
			if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
				return err
			}
		}
		event := audit.NewDomainEvent(ctx, tenantID, "service_account_updated", "service_account", id)
		event.Detail["status"] = status
		event.Detail["status_changed"] = statusChanged
		event.Detail["expires_at"] = expiresAt
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		current.Description, current.Status, current.ExpiresAt = description, status, expiresAt
		current.DisplayName, current.Version, current.UpdatedAt = displayName, version+1, now
		updated = serviceAccountFromRow(current)
		return nil
	})
	if err != nil {
		return ServiceAccount{}, fmt.Errorf("replace service account: %w", err)
	}
	return updated, nil
}

func (s *Service) DeleteServiceAccount(ctx context.Context, tenantID, id guid.ID, version int64) error {
	if tenantID.Zero() || id.Zero() || version < 1 {
		return ErrServiceAccountInvalid
	}
	now := identityTime(s.now())
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getServiceAccount(ctx, tenantID, id, true)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrServiceAccountConflict
		}
		verify, err := s.administrators.Protect(ctx, repo.writer(), tenantID)
		if err != nil {
			return err
		}
		if err := repo.deleteServiceAccount(ctx, tenantID, id, version, now); err != nil {
			return err
		}
		if err := repo.updatePrincipalRow(ctx, id, map[string]any{
			"status": "disabled", "disabled_at": &now, "updated_at": now,
		}); err != nil {
			return err
		}
		if err := repo.updateTenantMember(ctx, tenantID, id, map[string]any{
			"status": "disabled", "disabled_at": &now, "updated_at": now,
		}); err != nil {
			return err
		}
		if err := repo.revokeServiceAccountCredentials(ctx, id, now); err != nil {
			return err
		}
		if err := verify(); err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, audit.NewDomainEvent(
			ctx, tenantID, "service_account_deleted", "service_account", id,
		))
	})
	if err != nil {
		return fmt.Errorf("delete service account: %w", err)
	}
	return nil
}

func (s *Service) CreateServiceAccountCredential(
	ctx context.Context,
	tenantID, id guid.ID,
	name string,
	scopes []string,
	expiresAt *time.Time,
) (ServiceAccountCredentialSecret, error) {
	name = strings.TrimSpace(name)
	normalizedScopes, ok := normalizeServiceAccountScopes(scopes)
	if tenantID.Zero() || id.Zero() || name == "" || len(name) > 128 || !ok {
		return ServiceAccountCredentialSecret{}, ErrServiceAccountInvalid
	}
	now := identityTime(s.now())
	expiresAt = identityOptionalTime(expiresAt)
	if expiresAt != nil && !expiresAt.After(now) {
		return ServiceAccountCredentialSecret{}, ErrServiceAccountInvalid
	}
	credentialID, err := s.repo.nextID()
	if err != nil {
		return ServiceAccountCredentialSecret{}, err
	}
	secret, err := randomServiceAccountSecret()
	if err != nil {
		return ServiceAccountCredentialSecret{}, err
	}
	secretHash, err := passwordx.Hash(secret)
	if err != nil {
		return ServiceAccountCredentialSecret{}, err
	}
	row := model.ServiceAccountCredential{
		ID: credentialID, PrincipalID: id, Name: name, SecretHash: secretHash,
		Scopes: strings.Join(normalizedScopes, " "), ExpiresAt: expiresAt, CreatedAt: now,
	}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		account, err := repo.getServiceAccount(ctx, tenantID, id, true)
		if err != nil {
			return err
		}
		if account.Status != "active" || account.ExpiresAt != nil && !account.ExpiresAt.After(now) ||
			account.ExpiresAt != nil && row.ExpiresAt != nil && row.ExpiresAt.After(*account.ExpiresAt) {
			return ErrServiceAccountInvalid
		}
		count, err := repo.countActiveServiceAccountCredentials(ctx, id, now)
		if err != nil {
			return err
		}
		if count >= maxServiceAccountCredentials {
			return ErrCredentialLimit
		}
		if err := repo.insertServiceAccountCredential(ctx, &row); err != nil {
			return err
		}
		event := audit.NewDomainEvent(
			ctx, tenantID, "service_account_credential_created", "service_account_credential", row.ID,
		)
		event.Detail["service_account_id"] = id
		event.Detail["expires_at"] = row.ExpiresAt
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return ServiceAccountCredentialSecret{}, fmt.Errorf("create service account credential: %w", err)
	}
	return ServiceAccountCredentialSecret{
		Credential: serviceAccountCredentialFromRow(&row), Secret: secret,
	}, nil
}

func (s *Service) ListServiceAccountCredentials(
	ctx context.Context,
	tenantID, id guid.ID,
) ([]ServiceAccountCredential, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	if _, err := repo.getServiceAccount(ctx, tenantID, id, false); err != nil {
		return nil, err
	}
	rows, err := repo.listServiceAccountCredentials(ctx, id)
	if err != nil {
		return nil, err
	}
	items := make([]ServiceAccountCredential, len(rows))
	for index, row := range rows {
		items[index] = serviceAccountCredentialFromRow(row)
	}
	return items, nil
}

func (s *Service) RevokeServiceAccountCredential(
	ctx context.Context,
	tenantID, id, credentialID guid.ID,
) error {
	if tenantID.Zero() || id.Zero() || credentialID.Zero() {
		return ErrServiceAccountInvalid
	}
	now := identityTime(s.now())
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.getServiceAccount(ctx, tenantID, id, true); err != nil {
			return err
		}
		changed, err := repo.revokeServiceAccountCredential(ctx, id, credentialID, now)
		if err != nil || !changed {
			return err
		}
		if err := repo.incrementServiceAccountTokenVersion(ctx, tenantID, id, now); err != nil {
			return err
		}
		event := audit.NewDomainEvent(
			ctx, tenantID, "service_account_credential_revoked", "service_account_credential", credentialID,
		)
		event.Detail["service_account_id"] = id
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return fmt.Errorf("revoke service account credential: %w", err)
	}
	return nil
}

func (r *Repository) tenantActive(ctx context.Context, tenantID guid.ID) (bool, error) {
	_, db, err := r.tables()
	if err != nil {
		return false, err
	}
	var count int64
	err = db.WithContext(ctx).Table(r.table("iam_tenants")).
		Where("id = ? AND status = 'active'", tenantID).Count(&count).Error
	return count == 1, err
}

func (r *Repository) insertServiceAccount(ctx context.Context, row *model.ServiceAccount) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Table(tables.ServiceAccounts.TableName()).Create(row).Error; err != nil {
		return fmt.Errorf("insert service account: %w", err)
	}
	return nil
}

func (r *Repository) serviceAccountQuery(ctx context.Context) (*gorm.DB, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	return db.WithContext(ctx).Table(tables.ServiceAccounts.TableName() + " AS service_account").
		Select("service_account.*, principal.login_name AS login_name, principal.display_name AS display_name").
		Joins("JOIN " + tables.Principals.TableName() + " AS principal ON principal.id = service_account.principal_id"), nil
}

func (r *Repository) getServiceAccount(
	ctx context.Context,
	tenantID, id guid.ID,
	lock bool,
) (*serviceAccountView, error) {
	if tenantID.Zero() || id.Zero() {
		return nil, ErrServiceAccountInvalid
	}
	query, err := r.serviceAccountQuery(ctx)
	if err != nil {
		return nil, err
	}
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var row serviceAccountView
	err = query.Where(
		"service_account.owner_tenant_id = ? AND service_account.principal_id = ? AND service_account.status <> 'deleted'",
		tenantID, id,
	).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrServiceAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get service account: %w", err)
	}
	return &row, nil
}

func (r *Repository) listServiceAccounts(
	ctx context.Context,
	tenantID guid.ID,
	search string,
	limit, offset int,
) ([]*serviceAccountView, int64, error) {
	query, err := r.serviceAccountQuery(ctx)
	if err != nil {
		return nil, 0, err
	}
	query = query.Where("service_account.owner_tenant_id = ? AND service_account.status <> 'deleted'", tenantID)
	search = strings.ToLower(strings.TrimSpace(search))
	if search != "" {
		pattern := "%" + search + "%"
		query = query.Where(
			"LOWER(principal.login_name) LIKE ? OR LOWER(principal.display_name) LIKE ? OR LOWER(service_account.description) LIKE ?",
			pattern, pattern, pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count service accounts: %w", err)
	}
	rows := make([]*serviceAccountView, 0)
	if err := query.Order("principal.login_name ASC, service_account.principal_id ASC").
		Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list service accounts: %w", err)
	}
	return rows, total, nil
}

func (r *Repository) replaceServiceAccount(
	ctx context.Context,
	tenantID, id guid.ID,
	description, status string,
	expiresAt *time.Time,
	version int64,
	invalidateTokens bool,
	now time.Time,
) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	updates := map[string]any{
		"description": description, "status": status, "expires_at": expiresAt,
		"version": gorm.Expr("version + 1"), "updated_at": now,
	}
	if invalidateTokens {
		updates["token_version"] = gorm.Expr("token_version + 1")
	}
	result := db.WithContext(ctx).Table(tables.ServiceAccounts.TableName()).
		Where("owner_tenant_id = ? AND principal_id = ? AND version = ? AND status <> 'deleted'", tenantID, id, version).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("replace service account: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrServiceAccountConflict
	}
	return nil
}

func (r *Repository) deleteServiceAccount(
	ctx context.Context,
	tenantID, id guid.ID,
	version int64,
	now time.Time,
) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.ServiceAccounts.TableName()).
		Where("owner_tenant_id = ? AND principal_id = ? AND version = ? AND status <> 'deleted'", tenantID, id, version).
		Updates(map[string]any{
			"status": "deleted", "deleted_at": now, "updated_at": now,
			"version": gorm.Expr("version + 1"), "token_version": gorm.Expr("token_version + 1"),
		})
	if result.Error != nil {
		return fmt.Errorf("delete service account: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrServiceAccountConflict
	}
	return nil
}

func (r *Repository) revokeServiceAccountCredentials(ctx context.Context, id guid.ID, now time.Time) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.ServiceAccountCredentials.TableName()).
		Where("principal_id = ? AND revoked_at IS NULL", id).Update("revoked_at", now)
	return result.Error
}

func (r *Repository) countActiveServiceAccountCredentials(
	ctx context.Context,
	id guid.ID,
	now time.Time,
) (int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return 0, err
	}
	count, err := tables.ServiceAccountCredentials.WithContext(ctx).
		Where(
			tables.ServiceAccountCredentials.PrincipalID.Eq(id),
			tables.ServiceAccountCredentials.RevokedAt.IsNull(),
			field.Or(
				tables.ServiceAccountCredentials.ExpiresAt.IsNull(),
				tables.ServiceAccountCredentials.ExpiresAt.Gt(now),
			),
		).Count()
	return count, err
}

func (r *Repository) insertServiceAccountCredential(
	ctx context.Context,
	row *model.ServiceAccountCredential,
) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Table(tables.ServiceAccountCredentials.TableName()).Create(row).Error; err != nil {
		return fmt.Errorf("insert service account credential: %w", err)
	}
	return nil
}

func (r *Repository) listServiceAccountCredentials(
	ctx context.Context,
	id guid.ID,
) ([]*model.ServiceAccountCredential, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows, err := tables.ServiceAccountCredentials.WithContext(ctx).
		Where(tables.ServiceAccountCredentials.PrincipalID.Eq(id)).
		Order(tables.ServiceAccountCredentials.CreatedAt.Desc(), tables.ServiceAccountCredentials.ID).
		Find()
	if err != nil {
		return nil, fmt.Errorf("list service account credentials: %w", err)
	}
	return rows, nil
}

func (r *Repository) revokeServiceAccountCredential(
	ctx context.Context,
	id, credentialID guid.ID,
	now time.Time,
) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(tables.ServiceAccountCredentials.TableName()).
		Where("id = ? AND principal_id = ? AND revoked_at IS NULL", credentialID, id).
		Update("revoked_at", now)
	if result.Error != nil {
		return false, fmt.Errorf("revoke service account credential: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	var count int64
	if err := db.WithContext(ctx).Table(tables.ServiceAccountCredentials.TableName()).
		Where("id = ? AND principal_id = ?", credentialID, id).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check service account credential: %w", err)
	}
	if count == 0 {
		return false, ErrCredentialNotFound
	}
	return false, nil
}

func (r *Repository) incrementServiceAccountTokenVersion(
	ctx context.Context,
	tenantID, id guid.ID,
	now time.Time,
) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.ServiceAccounts.TableName()).
		Where("principal_id = ? AND owner_tenant_id = ? AND status <> 'deleted'", id, tenantID).
		Updates(map[string]any{"token_version": gorm.Expr("token_version + 1"), "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrServiceAccountNotFound
	}
	return nil
}

func validServiceAccount(
	tenantID guid.ID,
	loginName, displayName, description, status string,
	expiresAt *time.Time,
	now time.Time,
) bool {
	return !tenantID.Zero() && loginName != "" && len(loginName) <= 200 &&
		displayName != "" && len(displayName) <= 128 && len(description) <= 1000 &&
		(status == "active" || status == "disabled") && (expiresAt == nil || expiresAt.After(now))
}

func normalizeServiceAccountScopes(values []string) ([]string, bool) {
	if len(values) == 0 || len(values) > 128 {
		return nil, false
	}
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 || strings.ContainsAny(value, " \t\r\n") {
			return nil, false
		}
		unique[value] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, true
}

func serviceAccountFromRow(row *serviceAccountView) ServiceAccount {
	return ServiceAccount{
		ID: row.PrincipalID, LoginName: row.LoginName, DisplayName: row.DisplayName,
		Description: row.Description, Status: row.Status, ExpiresAt: identityOptionalTime(row.ExpiresAt),
		Version: row.Version, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
}

func serviceAccountCredentialFromRow(row *model.ServiceAccountCredential) ServiceAccountCredential {
	return ServiceAccountCredential{
		ID: row.ID, ServiceAccountID: row.PrincipalID, Name: row.Name,
		Scopes: strings.Fields(row.Scopes), ExpiresAt: identityOptionalTime(row.ExpiresAt),
		LastUsedAt: identityOptionalTime(row.LastUsedAt), RevokedAt: identityOptionalTime(row.RevokedAt),
		CreatedAt: row.CreatedAt.UTC(),
	}
}

func identityOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := identityTime(*value)
	return &normalized
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func randomServiceAccountSecret() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
