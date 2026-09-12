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
	// credentials is authn's capability for the credential tables authn owns.
	// identity sequences those writes inside its own transaction; it does not
	// run them.
	credentials PrincipalCredentialStore
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
	if r.credentials.CreateHashed == nil {
		return fmt.Errorf("%w: no credential store", errRepositoryUnavailable)
	}
	if err := r.credentials.CreateHashed(ctx, db, principalID, passwordHash, now); err != nil {
		return fmt.Errorf("insert principal credential: %w", err)
	}
	return nil
}

func (r *Repository) incrementCredentialVersion(ctx context.Context, principalID guid.ID, now time.Time) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	if r.credentials.IncrementVersion == nil {
		return fmt.Errorf("%w: no credential store", errRepositoryUnavailable)
	}
	found, err := r.credentials.IncrementVersion(ctx, db, principalID, now)
	if err != nil {
		return fmt.Errorf("increment principal credential version: %w", err)
	}
	// A principal with no credential at all is not an error authn can judge: a
	// federation-only principal legitimately has none, and what that means here
	// is identity's to say.
	if !found {
		return errPrincipalCredentialNotFound
	}
	return nil
}

func (r *Repository) revokeSessionsAndRefreshTokens(ctx context.Context, principalID guid.ID, now time.Time) error {
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	if r.credentials.RevokeSessions == nil {
		return fmt.Errorf("%w: no credential store", errRepositoryUnavailable)
	}
	// The sessions belong to authn and the refresh tokens to oauth, so neither
	// is named here: authn runs its own revocation in this transaction, and
	// oauth's name arrives from the set oauth published.
	if err := r.credentials.RevokeSessions(ctx, db, principalID, now); err != nil {
		return fmt.Errorf("revoke principal sessions: %w", err)
	}
	refreshTokens, err := r.oauthTable("iam_refresh_tokens")
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Table(refreshTokens).
		Where("principal_id = ? AND revoked_at IS NULL", principalID).Update("revoked_at", now)
	if result.Error != nil {
		return fmt.Errorf("revoke principal state in iam_refresh_tokens: %w", result.Error)
	}
	return nil
}

func (r *Repository) revokeAllSecurityState(ctx context.Context, principalID guid.ID, now time.Time) error {
	if err := r.revokeSessionsAndRefreshTokens(ctx, principalID, now); err != nil {
		return err
	}
	_, db, err := r.tables()
	if err != nil {
		return err
	}
	if r.credentials.ConsumeCredentialTokens == nil {
		return fmt.Errorf("%w: no credential store", errRepositoryUnavailable)
	}
	// The recovery and verification tokens are authn's too, so spending them is
	// authn's statement, run here in identity's transaction.
	if err := r.credentials.ConsumeCredentialTokens(ctx, db, principalID, now); err != nil {
		return fmt.Errorf("consume principal credential tokens: %w", err)
	}
	return nil
}
