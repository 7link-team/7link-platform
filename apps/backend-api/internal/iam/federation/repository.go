package federation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/federation/model"
	"7link.com/platform/internal/iam/federation/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type providerRow = model.Provider
type identityLinkRow = model.IdentityLink
type samlSPRow = model.SAMLServiceProvider
type samlKeyRow = model.SAMLIDPKey

var errRepositoryUnavailable = errors.New("federation repository unavailable")

func (r *Repository) claimReplay(ctx context.Context, key string, expiresAt, now time.Time) (bool, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return false, fmt.Errorf("%w: %w", errReplayUnavailable, err)
	}
	// Hash protocol input so identifiers are bounded and byte-exact on all
	// database collations, including MySQL's trailing-space comparisons.
	digest := sha256.Sum256([]byte(key))
	storedExpiry := expiresAt.UTC().Truncate(time.Millisecond)
	if storedExpiry.Before(expiresAt) {
		storedExpiry = storedExpiry.Add(time.Millisecond)
	}
	row := model.ReplayClaim{ClaimKey: hex.EncodeToString(digest[:]), ExpiresAt: storedExpiry, CreatedAt: now.UTC()}
	tables, _, err := repo.tables()
	if err != nil {
		return false, fmt.Errorf("%w: %w", errReplayUnavailable, err)
	}
	err = repo.db.WithContext(ctx).Table(tables.ReplayClaims.TableName()).Create(&row).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: record claim: %w", errReplayUnavailable, err)
	}
	// Check after the committed insert: a paused request may insert after a
	// sweep removed an expired claim. A shared clock rejects that late winner.
	millis, err := repo.replayNow(ctx)
	if err != nil {
		return false, fmt.Errorf("%w: read clock: %w", errReplayUnavailable, err)
	}
	return expiresAt.UnixMilli() > millis+1, nil
}

func (r *Repository) replayNow(ctx context.Context) (int64, error) {
	return gormx.DatabaseClockMillis(ctx, r.db)
}

func (r *Repository) sweepReplayClaims(ctx context.Context) error {
	repo, err := r.pinWriter()
	if err != nil {
		return err
	}
	millis, err := repo.replayNow(ctx)
	if err != nil {
		return err
	}
	// A one-millisecond margin covers the SQL clock expression's rounding.
	tables, _, err := repo.tables()
	if err != nil {
		return err
	}
	return repo.db.WithContext(ctx).Table(tables.ReplayClaims.TableName()).
		Where("expires_at <= ?", time.UnixMilli(millis-1).UTC()).Delete(&model.ReplayClaim{}).Error
}

type Repository struct {
	runtime    *appenv.Runtime
	principals identity.PrincipalStateStore
	db         *gorm.DB
	inTx       bool
	// foreign holds the names of the tables this repository reads but does
	// not own. It is a pointer so a transactional clone reads the same
	// publication as the repository it came from.
	foreign *foreignTables
}

func NewRepository(runtime *appenv.Runtime) *Repository {
	if runtime == nil {
		panic("federation repository requires runtime")
	}
	return &Repository{runtime: runtime, foreign: &foreignTables{}}
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

// tables returns every table federation owns bound to the deployment prefix,
// alongside the database the statements run on. It is the same shape the
// sibling IAM repositories use: the repository reads physical names only
// through the generated query objects the module resolver bound, never by
// assembling one itself.
func (r *Repository) tables() (*query.Tables, *gorm.DB, error) {
	db := r.writer()
	if db == nil {
		return nil, nil, errRepositoryUnavailable
	}
	return query.Bind(db, r.prefix()), db, nil
}

func (r *Repository) withDB(db *gorm.DB, inTx bool) *Repository {
	clone := *r
	clone.db, clone.inTx = db, inTx
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
		return errors.New("invalid federation transaction")
	}
	if r.inTx {
		return fn(r)
	}
	pinned, err := r.pinWriter()
	if err != nil {
		return err
	}
	return pinned.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(pinned.withDB(tx, true))
	})
}

func (r *Repository) listProviders(ctx context.Context, tenantID guid.ID) ([]providerRow, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows := make([]providerRow, 0)
	err = r.writer().WithContext(ctx).Table(tables.Providers.TableName()).
		Where("tenant_id = ?", tenantID).Order("name ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list identity providers: %w", err)
	}
	return rows, nil
}

func (r *Repository) getProvider(ctx context.Context, tenantID, id guid.ID) (providerRow, error) {
	var row providerRow
	tables, _, err := r.tables()
	if err != nil {
		return row, err
	}
	err = r.writer().WithContext(ctx).Table(tables.Providers.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrProviderNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get identity provider: %w", err)
	}
	return row, nil
}

func (r *Repository) getProviderByID(ctx context.Context, id guid.ID) (providerRow, error) {
	var row providerRow
	tables, _, err := r.tables()
	if err != nil {
		return row, err
	}
	err = r.writer().WithContext(ctx).Table(tables.Providers.TableName()).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrProviderNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get identity provider: %w", err)
	}
	return row, nil
}

func (r *Repository) insertProvider(ctx context.Context, row *providerRow) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	err = r.writer().WithContext(ctx).Table(tables.Providers.TableName()).Create(row).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrProviderIssuerExists
	}
	if err != nil {
		return fmt.Errorf("create identity provider: %w", err)
	}
	return nil
}

func (r *Repository) updateProvider(ctx context.Context, row *providerRow) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	result := r.writer().WithContext(ctx).Table(tables.Providers.TableName()).
		Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).
		Select("name", "provider_type", "issuer", "client_id", "client_secret_ciphertext", "scopes", "auto_provision", "default_role_id", "status", "updated_at").Updates(row)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return ErrProviderIssuerExists
	}
	if result.Error != nil {
		return fmt.Errorf("update identity provider: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrProviderNotFound
	}
	return nil
}

func (r *Repository) deleteProvider(ctx context.Context, tenantID, id guid.ID) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	result := r.writer().WithContext(ctx).Table(tables.Providers.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&providerRow{})
	if result.Error != nil {
		return fmt.Errorf("delete identity provider: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrProviderNotFound
	}
	return nil
}

func (r *Repository) identityLink(ctx context.Context, providerID, tenantID guid.ID, subject string) (identityLinkRow, error) {
	var row identityLinkRow
	tables, _, err := r.tables()
	if err != nil {
		return row, err
	}
	err = r.writer().WithContext(ctx).Table(tables.IdentityLinks.TableName()).
		Where("provider_id = ? AND tenant_id = ? AND external_subject = ?", providerID, tenantID, subject).Take(&row).Error
	return row, err
}

func (r *Repository) insertIdentityLink(ctx context.Context, row *identityLinkRow) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	if err := r.writer().WithContext(ctx).Table(tables.IdentityLinks.TableName()).Create(row).Error; err != nil {
		return fmt.Errorf("insert identity link: %w", err)
	}
	return nil
}

func (r *Repository) touchIdentityLink(ctx context.Context, providerID, tenantID guid.ID, subject string, now time.Time) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	result := r.writer().WithContext(ctx).Table(tables.IdentityLinks.TableName()).
		Where("provider_id = ? AND tenant_id = ? AND external_subject = ?", providerID, tenantID, subject).
		Updates(map[string]any{"last_login_at": now, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("update identity link login: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var count int64
	if err := r.writer().WithContext(ctx).Table(tables.IdentityLinks.TableName()).
		Where("provider_id = ? AND tenant_id = ? AND external_subject = ?", providerID, tenantID, subject).
		Count(&count).Error; err != nil {
		return fmt.Errorf("verify identity link login update: %w", err)
	}
	if count != 1 {
		return ErrPrincipalInactive
	}
	return nil
}

func (r *Repository) roleExists(ctx context.Context, tenantID, roleID guid.ID) (bool, error) {
	roles := r.foreign.access.Roles.String()
	var count int64
	err := r.writer().WithContext(ctx).Table(roles).Where("tenant_id = ? AND id = ?", tenantID, roleID).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("validate default role: %w", err)
	}
	return count == 1, nil
}

func (r *Repository) grantDefaultRole(ctx context.Context, tenantID, roleID, principalID guid.ID, now time.Time) (bool, error) {
	exists, err := r.roleExists(ctx, tenantID, roleID)
	if err != nil || !exists {
		return false, err
	}
	roleMembers := r.foreign.access.RoleMembers.String()
	revisions := r.foreign.access.PolicyRevisions.String()
	member := map[string]any{"tenant_id": tenantID, "role_id": roleID, "principal_id": principalID, "created_at": now}
	if err := r.writer().WithContext(ctx).Table(roleMembers).Create(member).Error; err != nil {
		return false, fmt.Errorf("grant federation default role: %w", err)
	}
	revision := map[string]any{"tenant_id": tenantID, "revision": int64(1), "updated_at": now}
	err = r.writer().WithContext(ctx).Table(revisions).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"revision":   gorm.Expr("? + 1", clause.Column{Table: revisions, Name: "revision"}),
			"updated_at": now,
		}),
	}).Create(revision).Error
	if err != nil {
		return false, fmt.Errorf("advance IAM policy revision: %w", err)
	}
	return true, nil
}

func (r *Repository) verifyPrincipalAndMember(ctx context.Context, tenantID, principalID guid.ID) error {
	status, found, err := r.principals.Status(ctx, r.writer(), principalID)
	if err != nil {
		return err
	}
	if !found || status != "active" {
		return ErrPrincipalInactive
	}
	members := r.foreign.access.TenantMembers.String()
	status = ""
	err = r.writer().WithContext(ctx).Table(members).Select("status").
		Where("tenant_id = ? AND principal_id = ?", tenantID, principalID).Scan(&status).Error
	if err != nil {
		return err
	}
	if status != "active" {
		return ErrPrincipalInactive
	}
	return nil
}

func (r *Repository) listSAMLServiceProviders(ctx context.Context, tenantID guid.ID) ([]samlSPRow, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows := make([]samlSPRow, 0)
	err = r.writer().WithContext(ctx).Table(tables.SAMLServiceProviders.TableName()).
		Where("tenant_id = ?", tenantID).Order("name ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list SAML service providers: %w", err)
	}
	return rows, nil
}

func (r *Repository) getSAMLServiceProvider(ctx context.Context, tenantID, id guid.ID) (samlSPRow, error) {
	var row samlSPRow
	tables, _, err := r.tables()
	if err != nil {
		return row, err
	}
	err = r.writer().WithContext(ctx).Table(tables.SAMLServiceProviders.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrSAMLSPNotFound
	}
	if err != nil {
		return row, fmt.Errorf("load SAML service provider: %w", err)
	}
	return row, nil
}

func (r *Repository) activeSAMLServiceProvider(ctx context.Context, tenantID guid.ID, entityID string) (samlSPRow, error) {
	var row samlSPRow
	tables, _, err := r.tables()
	if err != nil {
		return row, err
	}
	err = r.writer().WithContext(ctx).Table(tables.SAMLServiceProviders.TableName()).
		Where("tenant_id = ? AND entity_id = ? AND status = ?", tenantID, entityID, samlSPStatusActive).Take(&row).Error
	return row, err
}

func (r *Repository) insertSAMLServiceProvider(ctx context.Context, row *samlSPRow) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	err = r.writer().WithContext(ctx).Table(tables.SAMLServiceProviders.TableName()).Create(row).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrSAMLSPEntityIDExists
	}
	if err != nil {
		return fmt.Errorf("create SAML service provider: %w", err)
	}
	return nil
}

func (r *Repository) updateSAMLServiceProvider(ctx context.Context, row *samlSPRow) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	result := r.writer().WithContext(ctx).Table(tables.SAMLServiceProviders.TableName()).
		Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).
		Select("name", "entity_id", "metadata_xml", "status", "updated_at").Updates(row)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return ErrSAMLSPEntityIDExists
	}
	if result.Error != nil {
		return fmt.Errorf("update SAML service provider: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrSAMLSPNotFound
	}
	return nil
}

func (r *Repository) deleteSAMLServiceProvider(ctx context.Context, tenantID, id guid.ID) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	result := r.writer().WithContext(ctx).Table(tables.SAMLServiceProviders.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&samlSPRow{})
	if result.Error != nil {
		return fmt.Errorf("delete SAML service provider: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrSAMLSPNotFound
	}
	return nil
}

func (r *Repository) latestSAMLKey(ctx context.Context) (samlKeyRow, error) {
	var row samlKeyRow
	tables, _, err := r.tables()
	if err != nil {
		return row, err
	}
	err = r.writer().WithContext(ctx).Table(tables.SAMLIDPKeys.TableName()).
		Where("rotated_at IS NULL").Take(&row).Error
	return row, err
}

func (r *Repository) insertSAMLKey(ctx context.Context, row *samlKeyRow) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	if err := r.writer().WithContext(ctx).Table(tables.SAMLIDPKeys.TableName()).Create(row).Error; err != nil {
		return fmt.Errorf("persist SAML signing key: %w", err)
	}
	return nil
}

func (r *Repository) rotateSAMLKeys(ctx context.Context, row *samlKeyRow, now time.Time) error {
	return r.runInTx(ctx, func(tx *Repository) error {
		if err := tx.insertSAMLKey(ctx, row); err != nil {
			return fmt.Errorf("persist rotated SAML signing key: %w", err)
		}
		tables, _, err := tx.tables()
		if err != nil {
			return err
		}
		if err := tx.writer().WithContext(ctx).Table(tables.SAMLIDPKeys.TableName()).
			Where("key_id <> ? AND rotated_at IS NULL", row.KeyID).Update("rotated_at", now).Error; err != nil {
			return fmt.Errorf("retire SAML signing keys: %w", err)
		}
		return nil
	})
}
