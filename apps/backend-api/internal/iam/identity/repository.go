package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
)

var (
	errRepositoryUnavailable       = errors.New("identity repository unavailable")
	errPrincipalCredentialNotFound = errors.New("principal credential not found")
)

// Repository persists identity state through the runtime's current writer.
// A transactional clone pins one writer for the complete workflow.
type Repository struct {
	runtime   *appenv.Runtime
	ids       guid.Source
	appender  audit.Appender
	revisions *core.RevisionStore
	db        *gorm.DB
	inTx      bool
	// foreign holds the names of the tables this repository reads but does not
	// own. The module's wiring attaches the holder its connectors bind; a
	// repository built without one reports the published tables as missing
	// instead of reading a table nobody named.
	foreign *foreignTables
}

func NewRepository(runtime *appenv.Runtime, ids guid.Source, appender audit.Appender) *Repository {
	if runtime == nil || ids == nil || appender == nil {
		panic("identity repository requires runtime, id generator, and audit appender")
	}
	revisions, err := core.NewRevisionStore(runtime.TablePrefix())
	if err != nil {
		panic(err)
	}
	return &Repository{
		runtime: runtime, ids: ids, appender: appender, revisions: revisions,
		foreign: &foreignTables{},
	}
}

// tenantTable returns the physical name organization published for the tenant
// table. identity reads it without owning it, and a deployment where the owner
// never published gets this error rather than a statement against an empty
// table name.
func (r *Repository) tenantTable() (string, error) {
	if r == nil {
		return "", errRepositoryUnavailable
	}
	return r.foreign.directoryTable("iam_tenants")
}

// memberTable returns the physical name core published for the tenant member
// table. identity reads and writes membership rows without owning them, so a
// deployment where core never published gets this error rather than a
// statement against an empty table name.
func (r *Repository) memberTable() (string, error) {
	if r == nil {
		return "", errRepositoryUnavailable
	}
	return r.foreign.accessTable("iam_tenant_members")
}

// oauthTable returns the physical name oauth published for a logical table.
func (r *Repository) oauthTable(logical string) (string, error) {
	if r == nil {
		return "", errRepositoryUnavailable
	}
	return r.foreign.oauthTable(logical)
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

func (r *Repository) on(db *gorm.DB) (*Repository, error) {
	if r == nil || db == nil {
		return nil, errRepositoryUnavailable
	}
	return r.withDB(db, true), nil
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
		return errors.New("invalid identity transaction")
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

func (r *Repository) nextID() (guid.ID, error) { return nextIdentityID(r.ids) }

func nextIdentityID(ids guid.Source) (guid.ID, error) {
	if ids == nil {
		return 0, errRepositoryUnavailable
	}
	id, err := ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, errors.New("generated identity ID must be positive")
	}
	return guid.ID(id), nil
}

func (r *Repository) appendAudit(ctx context.Context, event audit.DomainEvent) error {
	if r == nil || r.appender == nil || r.writer() == nil {
		return errRepositoryUnavailable
	}
	if err := r.appender(ctx, r.writer(), event); err != nil {
		return fmt.Errorf("append identity audit event: %w", err)
	}
	return nil
}

func (r *Repository) bumpPolicyRevision(ctx context.Context, tenantID guid.ID, now time.Time) error {
	if r == nil || r.revisions == nil || r.writer() == nil {
		return errRepositoryUnavailable
	}
	return r.revisions.Bump(ctx, r.writer(), tenantID, now)
}

func (r *Repository) createCredential(ctx context.Context, principalID guid.ID, passwordHash string, now time.Time) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(r.table("iam_credentials")).Create(map[string]any{
		"principal_id": principalID, "password_hash": passwordHash,
		"password_changed_at": now, "credential_version": int64(1), "updated_at": now,
	})
	if result.Error != nil {
		return fmt.Errorf("insert principal credential: %w", result.Error)
	}
	return nil
}

func (r *Repository) incrementCredentialVersion(ctx context.Context, principalID guid.ID, now time.Time) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(r.table("iam_credentials")).Where("principal_id = ?", principalID).
		Updates(map[string]any{"credential_version": gorm.Expr("credential_version + 1"), "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("increment principal credential version: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return errPrincipalCredentialNotFound
	}
	return nil
}

func (r *Repository) revokeSessionsAndRefreshTokens(ctx context.Context, principalID guid.ID, now time.Time) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	for _, table := range []string{"iam_sessions", "iam_refresh_tokens"} {
		// Resolved before each write, so a name the owner never published stops
		// the revocation instead of running it against an empty table name.
		physical, err := r.revocationTable(table)
		if err != nil {
			return err
		}
		result := db.WithContext(ctx).Table(physical).
			Where("principal_id = ? AND revoked_at IS NULL", principalID).Update("revoked_at", now)
		if result.Error != nil {
			return fmt.Errorf("revoke principal state in %s: %w", table, result.Error)
		}
	}
	return nil
}

// revocationTable returns the physical name of a table a principal-state
// revocation writes. The sessions belong to identity, so its own prefix names
// them; the refresh tokens belong to oauth, so their name is the one the owner
// published.
func (r *Repository) revocationTable(logical string) (string, error) {
	if logical != "iam_refresh_tokens" {
		return r.table(logical), nil
	}
	return r.oauthTable(logical)
}

func (r *Repository) revokeAllSecurityState(ctx context.Context, principalID guid.ID, now time.Time) error {
	if err := r.revokeSessionsAndRefreshTokens(ctx, principalID, now); err != nil {
		return err
	}
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	for _, table := range []string{"iam_password_recovery_tokens", "iam_email_verification_tokens"} {
		result := db.WithContext(ctx).Table(r.table(table)).
			Where("principal_id = ? AND consumed_at IS NULL", principalID).Update("consumed_at", now)
		if result.Error != nil {
			return fmt.Errorf("consume principal state in %s: %w", table, result.Error)
		}
	}
	return nil
}
