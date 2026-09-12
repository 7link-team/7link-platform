package authn

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
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

func (r *Repository) findLoginPrincipal(ctx context.Context, loginName string) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	if r.principals.HumanByLogin == nil {
		return nil, errRepositoryUnavailable
	}
	row, found, err := r.principals.HumanByLogin(ctx, db, loginName)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func (r *Repository) getActivePrincipal(ctx context.Context, principalID guid.ID) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	if r.principals.ActiveRow == nil {
		return nil, errRepositoryUnavailable
	}
	row, found, err := r.principals.ActiveRow(ctx, db, principalID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func (r *Repository) findRecoveryPrincipal(ctx context.Context, identifier string) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	if r.principals.ActiveHumanByIdentifier == nil {
		return nil, errRepositoryUnavailable
	}
	// Identity returns every match; which count means what is authn's recovery
	// policy, so the decision stays here.
	rows, err := r.principals.ActiveHumanByIdentifier(ctx, db, identifier)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(rows) != 1 {
		return nil, errAmbiguousRecoveryPrincipal
	}
	return &rows[0], nil
}

func (r *Repository) lockPrincipal(ctx context.Context, principalID guid.ID) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	if r.principals.LockActiveRow == nil {
		return nil, errRepositoryUnavailable
	}
	row, found, err := r.principals.LockActiveRow(ctx, db, principalID)
	if err != nil {
		return nil, err
	}
	// Every caller but one keeps only the error and relies on an absent or
	// suspended principal stopping the operation, so a missing row has to be an
	// error here and not a zero value.
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func (r *Repository) lockPrincipalByLogin(ctx context.Context, loginName string) (*identity.Principal, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	if r.principals.LockByLogin == nil {
		return nil, errRepositoryUnavailable
	}
	row, found, err := r.principals.LockByLogin(ctx, db, loginName)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func (r *Repository) verifyPrincipalEmail(ctx context.Context, principalID guid.ID, email string, now time.Time) (bool, error) {
	_, db, err := r.tables()
	if err != nil {
		return false, err
	}
	if r.principals.VerifyEmail == nil {
		return false, errRepositoryUnavailable
	}
	return r.principals.VerifyEmail(ctx, db, principalID, email, now)
}

func (r *Repository) principalState(ctx context.Context, principalID guid.ID) (principalCredentialState, error) {
	tables, db, err := r.tables()
	if err != nil {
		return principalCredentialState{}, err
	}
	principalTable := r.foreign.principal.Principals.String()
	var state principalCredentialState
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
	accountTable := r.foreign.principal.ServiceAccounts.String()
	principalTable := r.foreign.principal.Principals.String()
	memberTable := r.foreign.access.TenantMembers.String()
	tenantTable := r.foreign.directory.Tenants.String()
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
	clientTable := r.foreign.oauth.Clients.String()
	var state oauthClientState
	err = db.WithContext(ctx).Table(clientTable).
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
	principalTable := r.foreign.principal.Principals.String()
	err = db.WithContext(ctx).Table(principalTable+" AS principal").
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
