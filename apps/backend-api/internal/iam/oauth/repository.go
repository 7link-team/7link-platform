package oauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/iam/oauth/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errRepositoryUnavailable = errors.New("OAuth repository unavailable")

// Repository persists OAuth state through the runtime's current writer. A
// transactional clone pins one writer for the complete protocol operation.
type Repository struct {
	runtime    *appenv.Runtime
	ids        guid.Source
	appender   audit.Appender
	principals identity.PrincipalStateStore
	db         *gorm.DB
	inTx       bool
	// foreign holds the names of the tables this repository reads but does
	// not own. It is a pointer so a transactional clone reads the same
	// publication as the repository it came from.
	foreign *foreignTables
}

func NewRepository(runtime *appenv.Runtime, ids guid.Source, appender audit.Appender) *Repository {
	if runtime == nil || ids == nil || appender == nil {
		panic("OAuth repository requires runtime, id generator, and audit appender")
	}
	return &Repository{runtime: runtime, ids: ids, appender: appender, foreign: &foreignTables{}}
}

func (r *Repository) writer() *gorm.DB {
	if r == nil {
		return nil
	}
	if r.db != nil {
		return r.db
	}
	if r.runtime == nil || r.runtime.Database() == nil {
		return nil
	}
	return r.runtime.Database().Write()
}

func (r *Repository) prefix() string {
	if r == nil || r.runtime == nil {
		return ""
	}
	return r.runtime.TablePrefix()
}

func (r *Repository) table(name string) string { return r.prefix() + name }

func (r *Repository) tables() (*query.Tables, *gorm.DB, error) {
	db := r.writer()
	if db == nil {
		return nil, nil, errRepositoryUnavailable
	}
	return query.Bind(db, r.prefix()), db, nil
}

func (r *Repository) withDB(db *gorm.DB, inTx bool) *Repository {
	clone := *r
	clone.db = db
	clone.inTx = inTx
	return &clone
}

func (r *Repository) pinWriter() (*Repository, error) {
	if r == nil {
		return nil, errRepositoryUnavailable
	}
	if r.db != nil {
		return r, nil
	}
	db := r.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	return r.withDB(db, false), nil
}

func (r *Repository) runInTx(ctx context.Context, fn func(*Repository) error) error {
	if r == nil || fn == nil {
		return errors.New("invalid OAuth transaction")
	}
	if r.inTx {
		return fn(r)
	}
	db := r.writer()
	if db == nil {
		return errRepositoryUnavailable
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(r.withDB(tx, true))
	})
}

func (r *Repository) nextID() (guid.ID, error) {
	if r == nil || r.ids == nil {
		return 0, errRepositoryUnavailable
	}
	id, err := r.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, errors.New("generated OAuth ID must be positive")
	}
	return guid.ID(id), nil
}

func (r *Repository) appendAudit(ctx context.Context, event audit.DomainEvent) error {
	db := r.writer()
	if r == nil || r.appender == nil || db == nil {
		return errRepositoryUnavailable
	}
	if err := r.appender(ctx, db, event); err != nil {
		return fmt.Errorf("append OAuth audit event: %w", err)
	}
	return nil
}

func (r *Repository) insertClient(ctx context.Context, row *model.Client) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Table(tables.Clients.TableName()).Create(row).Error; err != nil {
		return fmt.Errorf("insert OAuth client: %w", err)
	}
	return nil
}

func (r *Repository) listClients(ctx context.Context, tenantID guid.ID) ([]*model.Client, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows := make([]*model.Client, 0)
	if err := db.WithContext(ctx).Table(tables.Clients.TableName()).
		Where("tenant_id = ?", tenantID).Order("name ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list OAuth clients: %w", err)
	}
	return rows, nil
}

func (r *Repository) updateClient(
	ctx context.Context,
	tenantID, id guid.ID,
	updates map[string]any,
) (*model.Client, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	result := db.WithContext(ctx).Table(tables.Clients.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Updates(updates)
	if result.Error != nil {
		return nil, fmt.Errorf("update OAuth client: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, ErrInvalidRequest
	}
	var row model.Client
	if err := db.WithContext(ctx).Table(tables.Clients.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error; err != nil {
		return nil, fmt.Errorf("read updated OAuth client: %w", err)
	}
	return &row, nil
}

func (r *Repository) revokeClientRefreshTokens(ctx context.Context, clientID guid.ID, now time.Time) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.RefreshTokens.TableName()).
		Where("client_id = ? AND revoked_at IS NULL", clientID).Update("revoked_at", now)
	if result.Error != nil {
		return fmt.Errorf("revoke OAuth client refresh tokens: %w", result.Error)
	}
	return nil
}

func (r *Repository) deleteClient(ctx context.Context, tenantID, id guid.ID) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.Clients.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&model.Client{})
	if result.Error != nil {
		return fmt.Errorf("delete OAuth client: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrInvalidRequest
	}
	return nil
}

func (r *Repository) activeClient(ctx context.Context, id guid.ID) (*model.Client, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row model.Client
	err = db.WithContext(ctx).Table(tables.Clients.TableName()).
		Where("id = ? AND status = ?", id, "active").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidRequest
	}
	if err != nil {
		return nil, fmt.Errorf("read OAuth client: %w", err)
	}
	return &row, nil
}

func (r *Repository) activeMember(ctx context.Context, tenantID, principalID guid.ID) (bool, error) {
	_, db, err := r.tables()
	if err != nil {
		return false, err
	}
	members := r.foreign.access.TenantMembers.String()
	var count int64
	err = db.WithContext(ctx).Table(members).
		Where("tenant_id = ? AND principal_id = ? AND status = ?", tenantID, principalID, "active").
		Count(&count).Error
	return count == 1, err
}

func (r *Repository) consent(ctx context.Context, clientID, principalID guid.ID) (*model.Consent, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row model.Consent
	err = db.WithContext(ctx).Table(tables.Consents.TableName()).
		Where("principal_id = ? AND client_id = ?", principalID, clientID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OAuth consent: %w", err)
	}
	return &row, nil
}

func (r *Repository) upsertConsent(ctx context.Context, row *model.Consent) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	err = db.WithContext(ctx).Table(tables.Consents.TableName()).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "principal_id"}, {Name: "client_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"tenant_id", "scope", "last_used_at"}),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("write OAuth consent: %w", err)
	}
	return nil
}

// lockConsent serializes grant issuance, consumption and withdrawal on the
// existing consent row. A no-op UPDATE also acquires SQLite's writer lock.
func (r *Repository) lockConsent(ctx context.Context, principalID, clientID guid.ID) (*model.Consent, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Table(tables.Consents.TableName()).
		Where("principal_id = ? AND client_id = ?", principalID, clientID).
		Update("last_used_at", gorm.Expr("last_used_at")).Error; err != nil {
		return nil, err
	}
	return r.consent(ctx, clientID, principalID)
}

func (r *Repository) requireGrant(ctx context.Context, principalID, clientID guid.ID, grant string) error {
	if grant == "" {
		return ErrInvalidRequest
	}
	row, err := r.lockConsent(ctx, principalID, clientID)
	if err != nil {
		return err
	}
	if row == nil || row.GrantID != grant {
		return ErrInvalidRequest
	}
	return nil
}

// grantedConsent is one standing consent joined to the client it names. A list
// somebody is meant to act on has to identify the application, and the client
// row is the only place its name lives.
type grantedConsent struct {
	ClientID   guid.ID   `gorm:"column:client_id"`
	TenantID   guid.ID   `gorm:"column:tenant_id"`
	ClientName string    `gorm:"column:client_name"`
	Scope      string    `gorm:"column:scope"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	LastUsedAt time.Time `gorm:"column:last_used_at"`
}

func (r *Repository) consentsForPrincipal(ctx context.Context, principalID guid.ID) ([]grantedConsent, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows := make([]grantedConsent, 0)
	err = db.WithContext(ctx).Table(tables.Consents.TableName()+" AS consent").
		Select("consent.client_id AS client_id, consent.tenant_id AS tenant_id, consent.scope AS scope, "+
			"consent.created_at AS created_at, consent.last_used_at AS last_used_at, "+
			"client.name AS client_name").
		Joins("JOIN "+tables.Clients.TableName()+" AS client ON client.id = consent.client_id").
		Where("consent.principal_id = ?", principalID).
		Order("consent.last_used_at DESC, consent.client_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list OAuth consents: %w", err)
	}
	return rows, nil
}

func (r *Repository) deleteConsent(ctx context.Context, principalID, clientID guid.ID) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.Consents.TableName()).
		Where("principal_id = ? AND client_id = ?", principalID, clientID).Delete(&model.Consent{})
	if result.Error != nil {
		return fmt.Errorf("delete OAuth consent: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrInvalidRequest
	}
	return nil
}

// revokeGrantedTokens withdraws everything one consent has already handed out:
// the refresh tokens issued under it, and the authorization codes still waiting
// to be exchanged. A code issued seconds before the revocation becomes a live
// refresh token the moment it is redeemed, so leaving those alone would let the
// grant reappear on its own a minute after somebody took it back.
func (r *Repository) revokeGrantedTokens(ctx context.Context, principalID, clientID guid.ID, now time.Time) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.RefreshTokens.TableName()).
		Where("principal_id = ? AND client_id = ? AND revoked_at IS NULL", principalID, clientID).
		Update("revoked_at", now)
	if result.Error != nil {
		return fmt.Errorf("revoke OAuth consent refresh tokens: %w", result.Error)
	}
	result = db.WithContext(ctx).Table(tables.AuthorizationCodes.TableName()).
		Where("principal_id = ? AND client_id = ? AND consumed_at IS NULL", principalID, clientID).
		Update("consumed_at", now)
	if result.Error != nil {
		return fmt.Errorf("revoke OAuth consent authorization codes: %w", result.Error)
	}
	return nil
}

func (r *Repository) insertConsentRequest(ctx context.Context, row *model.ConsentRequest) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Table(tables.ConsentRequests.TableName()).Create(row).Error; err != nil {
		return fmt.Errorf("store consent request: %w", err)
	}
	return nil
}

func (r *Repository) consentRequest(ctx context.Context, challengeHash string) (*model.ConsentRequest, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row model.ConsentRequest
	err = db.WithContext(ctx).Table(tables.ConsentRequests.TableName()).
		Where("challenge_hash = ?", challengeHash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidRequest
	}
	if err != nil {
		return nil, fmt.Errorf("read consent request: %w", err)
	}
	return &row, nil
}

// resolveConsentRequest marks one pending request answered. The unresolved
// predicate is the whole point: it makes a challenge single-use, so a replayed
// decision cannot mint a second authorization code.
func (r *Repository) resolveConsentRequest(ctx context.Context, challengeHash string, now time.Time) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.ConsentRequests.TableName()).
		Where("challenge_hash = ? AND resolved_at IS NULL", challengeHash).Update("resolved_at", now)
	if result.Error != nil {
		return fmt.Errorf("resolve consent request: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrInvalidRequest
	}
	return nil
}

func (r *Repository) insertAuthorizationCode(ctx context.Context, row *model.AuthorizationCode) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Table(tables.AuthorizationCodes.TableName()).Create(row).Error; err != nil {
		return fmt.Errorf("store authorization code: %w", err)
	}
	return nil
}

func (r *Repository) authorizationCode(
	ctx context.Context,
	codeHash string,
	clientID guid.ID,
) (*model.AuthorizationCode, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row model.AuthorizationCode
	err = db.WithContext(ctx).Table(tables.AuthorizationCodes.TableName()).
		Where("code_hash = ? AND client_id = ?", codeHash, clientID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidRequest
	}
	if err != nil {
		return nil, fmt.Errorf("read authorization code: %w", err)
	}
	return &row, nil
}

func (r *Repository) consumeAuthorizationCode(
	ctx context.Context,
	codeHash string,
	now time.Time,
	refresh *model.RefreshToken,
) error {
	// Read the immutable reference before opening the transaction so its first
	// operation acquires the consent write lock (also safe for SQLite WAL).
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	var code model.AuthorizationCode
	if err := db.WithContext(ctx).Table(tables.AuthorizationCodes.TableName()).Where("code_hash = ?", codeHash).Take(&code).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvalidRequest
		}
		return err
	}
	return r.runInTx(ctx, func(tx *Repository) error {
		// Lock the current incarnation before codes, matching RevokeConsent.
		if err := tx.requireGrant(ctx, code.PrincipalID, code.ClientID, code.GrantID); err != nil {
			return err
		}
		tables, db, err := tx.tables()
		if err != nil {
			return err
		}
		result := db.WithContext(ctx).Table(tables.AuthorizationCodes.TableName()).
			Where("code_hash = ? AND consumed_at IS NULL", codeHash).Update("consumed_at", now)
		if result.Error != nil {
			return fmt.Errorf("consume authorization code: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrInvalidRequest
		}
		if refresh != nil {
			if err := db.WithContext(ctx).Table(tables.RefreshTokens.TableName()).Create(refresh).Error; err != nil {
				return fmt.Errorf("store refresh token: %w", err)
			}
		}
		return nil
	})
}

func (r *Repository) refreshToken(ctx context.Context, idHash string, clientID guid.ID) (*model.RefreshToken, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	var row model.RefreshToken
	err = db.WithContext(ctx).Table(tables.RefreshTokens.TableName()).
		Where("id_hash = ? AND client_id = ?", idHash, clientID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidRequest
	}
	if err != nil {
		return nil, fmt.Errorf("read refresh token: %w", err)
	}
	return &row, nil
}

func (r *Repository) rotateRefreshToken(
	ctx context.Context,
	idHash string,
	familyID string,
	now time.Time,
	next *model.RefreshToken,
) error {
	replayed := false
	err := r.runInTx(ctx, func(tx *Repository) error {
		if next == nil {
			return ErrInvalidRequest
		}
		if err := tx.requireGrant(ctx, next.PrincipalID, next.ClientID, next.GrantID); err != nil {
			return err
		}
		tables, db, err := tx.tables()
		if err != nil {
			return err
		}
		result := db.WithContext(ctx).Table(tables.RefreshTokens.TableName()).
			Where("id_hash = ? AND used_at IS NULL AND revoked_at IS NULL", idHash).
			Update("used_at", now)
		if result.Error != nil {
			return fmt.Errorf("consume refresh token: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			result = db.WithContext(ctx).Table(tables.RefreshTokens.TableName()).
				Where("family_id = ? AND revoked_at IS NULL", familyID).Update("revoked_at", now)
			if result.Error != nil {
				return fmt.Errorf("revoke replayed refresh token family: %w", result.Error)
			}
			replayed = true
			return nil
		}
		if err := db.WithContext(ctx).Table(tables.RefreshTokens.TableName()).Create(next).Error; err != nil {
			return fmt.Errorf("store refresh token: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if replayed {
		return ErrInvalidRequest
	}
	return nil
}

func (r *Repository) revokeRefreshFamily(ctx context.Context, familyID string, now time.Time) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(tables.RefreshTokens.TableName()).
		Where("family_id = ? AND revoked_at IS NULL", familyID).Update("revoked_at", now)
	if result.Error != nil {
		return fmt.Errorf("revoke refresh token family: %w", result.Error)
	}
	return nil
}

type serviceAccountClient struct {
	CredentialID        guid.ID    `gorm:"column:credential_id"`
	PrincipalID         guid.ID    `gorm:"column:principal_id"`
	TenantID            guid.ID    `gorm:"column:tenant_id"`
	LoginName           string     `gorm:"column:login_name"`
	SecretHash          string     `gorm:"column:secret_hash"`
	Scopes              string     `gorm:"column:scopes"`
	CredentialExpiresAt *time.Time `gorm:"column:credential_expires_at"`
	CredentialRevokedAt *time.Time `gorm:"column:credential_revoked_at"`
	AccountExpiresAt    *time.Time `gorm:"column:account_expires_at"`
	TokenVersion        int64      `gorm:"column:token_version"`
	AccountStatus       string     `gorm:"column:account_status"`
	PrincipalStatus     string     `gorm:"column:principal_status"`
	MemberStatus        string     `gorm:"column:member_status"`
	TenantStatus        string     `gorm:"column:tenant_status"`
}

func (r *Repository) serviceAccountClient(ctx context.Context, id guid.ID) (*serviceAccountClient, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	credentials := r.foreign.principal.ServiceAccountCredentials.String()
	accounts := r.foreign.principal.ServiceAccounts.String()
	principals := r.foreign.principal.Principals.String()
	members := r.foreign.access.TenantMembers.String()
	tenants := r.foreign.directory.Tenants.String()
	var row serviceAccountClient
	err = db.WithContext(ctx).Table(credentials+" AS credential").
		Select("credential.id AS credential_id, credential.principal_id AS principal_id, "+
			"credential.secret_hash AS secret_hash, credential.scopes AS scopes, "+
			"credential.expires_at AS credential_expires_at, credential.revoked_at AS credential_revoked_at, "+
			"service_account.owner_tenant_id AS tenant_id, service_account.status AS account_status, "+
			"service_account.expires_at AS account_expires_at, service_account.token_version AS token_version, "+
			"principal.login_name AS login_name, principal.status AS principal_status, "+
			"member.status AS member_status, tenant.status AS tenant_status").
		Joins("JOIN "+accounts+" AS service_account ON service_account.principal_id = credential.principal_id").
		Joins("JOIN "+principals+" AS principal ON principal.id = service_account.principal_id").
		Joins("JOIN "+members+" AS member ON member.tenant_id = service_account.owner_tenant_id AND member.principal_id = service_account.principal_id").
		Joins("JOIN "+tenants+" AS tenant ON tenant.id = service_account.owner_tenant_id").
		Where("credential.id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errServiceAccountCredentialMissing
	}
	if err != nil {
		return nil, fmt.Errorf("read service account credential: %w", err)
	}
	return &row, nil
}

func (r *Repository) touchServiceAccountCredential(ctx context.Context, id guid.ID, now time.Time) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	credentials := r.foreign.principal.ServiceAccountCredentials.String()
	result := db.WithContext(ctx).Table(credentials).
		Where("id = ? AND revoked_at IS NULL", id).Update("last_used_at", now)
	if result.Error != nil {
		return fmt.Errorf("update service account credential use: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrInvalidRequest
	}
	return nil
}
