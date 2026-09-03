package authn

import (
	"context"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errAmbiguousRecoveryPrincipal = errors.New("ambiguous password recovery principal")

type principalCredentialState struct {
	Status            string
	CredentialVersion int64
}

type serviceAccountState struct {
	TenantID        guid.ID
	AccountStatus   string
	PrincipalStatus string
	MemberStatus    string
	TenantStatus    string
	ExpiresAt       *time.Time
	TokenVersion    int64
}

type oauthClientState struct {
	TenantID guid.ID
	Status   string
}

const (
	principalTableName      = "iam_principals"
	serviceAccountTableName = "iam_service_accounts"
)

func (r *Repository) createPrincipal(ctx context.Context, row *identity.Principal) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Table(r.table(principalTableName)).Create(row).Error
}

func (r *Repository) updateBootstrapPrincipal(
	ctx context.Context,
	principalID guid.ID,
	email, displayName string,
	emailVerified bool,
	now time.Time,
) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Table(r.table(principalTableName)).
		Where("id = ?", principalID).
		Updates(map[string]any{
			"email":               email,
			"email_verified":      emailVerified,
			"activation_required": false,
			"display_name":        displayName,
			"status":              "active",
			"disabled_at":         nil,
			"updated_at":          now,
		}).Error
}

func (r *Repository) findLoginPrincipal(ctx context.Context, loginName string) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row identity.Principal
	principalTable := r.table(principalTableName)
	serviceAccountTable := r.table(serviceAccountTableName)
	err = db.WithContext(ctx).Table(principalTable+" AS principal").
		Where("principal.login_name = ?", loginName).
		Where("NOT EXISTS (?)", db.Table(serviceAccountTable+" AS service_account").
			Select("1").Where("service_account.principal_id = principal.id")).
		Take(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) getPrincipal(ctx context.Context, principalID guid.ID) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row identity.Principal
	if err := db.WithContext(ctx).Table(r.table(principalTableName)).Where("id = ?", principalID).Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) getActivePrincipal(ctx context.Context, principalID guid.ID) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row identity.Principal
	if err := db.WithContext(ctx).Table(r.table(principalTableName)).
		Where("id = ? AND status = ?", principalID, "active").Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) findRecoveryPrincipal(ctx context.Context, identifier string) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	principalTable := r.table(principalTableName)
	serviceAccountTable := r.table(serviceAccountTableName)
	var rows []*identity.Principal
	err = db.WithContext(ctx).Table(principalTable+" AS principal").
		Where("status = ? AND email_verified = ?", "active", true).
		Where("LOWER(principal.login_name) = ? OR LOWER(principal.email) = ?", identifier, identifier).
		Where("NOT EXISTS (?)", db.Table(serviceAccountTable+" AS service_account").
			Select("1").Where("service_account.principal_id = principal.id")).
		Limit(2).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(rows) != 1 {
		return nil, errAmbiguousRecoveryPrincipal
	}
	return rows[0], nil
}

func (r *Repository) lockPrincipal(ctx context.Context, principalID guid.ID) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row identity.Principal
	query := db.WithContext(ctx).Table(r.table(principalTableName)).Where("id = ? AND status = ?", principalID, "active")
	if db.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) lockPrincipalByLogin(ctx context.Context, loginName string) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row identity.Principal
	query := db.WithContext(ctx).Table(r.table(principalTableName)).Where("login_name = ?", loginName)
	if db.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) verifyPrincipalEmail(ctx context.Context, principalID guid.ID, email string, now time.Time) (bool, error) {
	_, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(r.table(principalTableName)).
		Where("id = ? AND status = ? AND email = ? AND email_verified = ?", principalID, "active", email, false).
		Updates(map[string]any{"email_verified": true, "activation_required": false, "updated_at": now})
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) principalState(ctx context.Context, principalID guid.ID) (principalCredentialState, error) {
	tables, db, err := r.tables()
	if err != nil {
		return principalCredentialState{}, err
	}
	var state principalCredentialState
	principalTable := r.table(principalTableName)
	err = db.WithContext(ctx).Table(principalTable+" AS principal").
		Select("principal.status AS status, COALESCE(credential.credential_version, 0) AS credential_version").
		Joins("LEFT JOIN "+tables.Credentials.TableName()+" AS credential ON credential.principal_id = principal.id").
		Where("principal.id = ?", principalID).Take(&state).Error
	return state, err
}

func (r *Repository) serviceAccountState(ctx context.Context, principalID guid.ID) (serviceAccountState, error) {
	_, db, err := r.tables()
	if err != nil {
		return serviceAccountState{}, err
	}
	accountTable := r.table(serviceAccountTableName)
	principalTable := r.table(principalTableName)
	memberTable := r.table("iam_tenant_members")
	tenantTable := r.table("iam_tenants")
	var state serviceAccountState
	err = db.WithContext(ctx).Table(accountTable+" AS account").
		Select("account.owner_tenant_id AS tenant_id, account.status AS account_status, account.expires_at, account.token_version, "+
			"principal.status AS principal_status, member.status AS member_status, tenant.status AS tenant_status").
		Joins("JOIN "+principalTable+" AS principal ON principal.id = account.principal_id").
		Joins("JOIN "+memberTable+" AS member ON member.tenant_id = account.owner_tenant_id AND member.principal_id = account.principal_id").
		Joins("JOIN "+tenantTable+" AS tenant ON tenant.id = account.owner_tenant_id").
		Where("account.principal_id = ?", principalID).Take(&state).Error
	return state, err
}

func (r *Repository) oauthClientState(ctx context.Context, clientID guid.ID) (oauthClientState, error) {
	_, db, err := r.tables()
	if err != nil {
		return oauthClientState{}, err
	}
	var state oauthClientState
	err = db.WithContext(ctx).Table(r.table("iam_oauth_clients")).
		Select("tenant_id, status").Where("id = ?", clientID).Take(&state).Error
	return state, err
}

func (r *Repository) activePrincipalClaims(ctx context.Context, principalID guid.ID) (*identity.Principal, int64, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	var row struct {
		identity.Principal `gorm:"embedded"`
		CredentialVersion  int64 `gorm:"column:credential_version"`
	}
	err = db.WithContext(ctx).Table(r.table(principalTableName)+" AS principal").
		Select("principal.id, principal.login_name, principal.email, principal.email_verified, "+
			"principal.activation_required, principal.display_name, principal.status, principal.created_at, "+
			"principal.updated_at, COALESCE(credential.credential_version, 0) AS credential_version").
		Joins("LEFT JOIN "+tables.Credentials.TableName()+" AS credential ON credential.principal_id = principal.id").
		Where("principal.id = ? AND principal.status = ? AND principal.activation_required = ?", principalID, "active", false).
		Take(&row).Error
	if err != nil {
		return nil, 0, err
	}
	return &row.Principal, row.CredentialVersion, nil
}
