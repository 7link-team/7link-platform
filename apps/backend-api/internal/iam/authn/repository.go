package authn

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

var errRepositoryUnavailable = errors.New("authentication repository unavailable")

// Repository persists authentication state through the runtime's current
// writer. A transactional clone pins one *gorm.DB for the complete workflow.
type Repository struct {
	runtime *appenv.Runtime
	ids     guid.Source
	audit   audit.Appender
	db      *gorm.DB
	inTx    bool
	// foreign holds the names of the tables this repository reads but does not
	// own. The module's wiring attaches the holder its connectors bind; a
	// repository built without one reports the published tables as missing
	// instead of reading a table nobody named.
	foreign *foreignTables
}

func NewRepository(runtime *appenv.Runtime, ids guid.Source, appender audit.Appender) *Repository {
	if runtime == nil || ids == nil || appender == nil {
		panic("authentication repository requires runtime, id generator, and audit appender")
	}
	return &Repository{runtime: runtime, ids: ids, audit: appender, foreign: &foreignTables{}}
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

// tenantTable returns the physical name organization published for the tenant
// table. authn reads it without owning it, and a deployment where the owner
// never published gets this error rather than a statement against an empty
// table name.
func (r *Repository) tenantTable() (string, error) {
	if r == nil {
		return "", errRepositoryUnavailable
	}
	return r.foreign.directoryTable("iam_tenants")
}

// BindForeignTables records the table-name sets of the tables this repository
// reads but does not own. The modules that own them reach it through the
// connectors authn publishes and the receiver identity pushes into; a caller
// that builds a repository outside the module has to call it itself, because an
// unbound repository reports the publication as missing rather than running a
// statement against an empty table name.
func (r *Repository) BindForeignTables(directory, oauth, access, principal gormx.TableSet) error {
	if r == nil {
		return errRepositoryUnavailable
	}
	if r.foreign == nil {
		r.foreign = &foreignTables{}
	}
	return r.foreign.bind(directory, oauth, access, principal)
}

// principalTable returns the physical name identity published for the principal
// table, and serviceAccountTable the one it published for service accounts.
// authn JOINs both without owning either, so a deployment where identity never
// published gets this error rather than a statement against an empty name.
func (r *Repository) principalTable() (string, error) {
	if r == nil {
		return "", errRepositoryUnavailable
	}
	return r.foreign.principalTable("iam_principals")
}

func (r *Repository) serviceAccountTable() (string, error) {
	if r == nil {
		return "", errRepositoryUnavailable
	}
	return r.foreign.principalTable("iam_service_accounts")
}

// oauthTable returns the physical name oauth published for a logical table.
func (r *Repository) oauthTable(logical string) (string, error) {
	if r == nil {
		return "", errRepositoryUnavailable
	}
	return r.foreign.oauthTable(logical)
}

func (r *Repository) tables() (*query.Tables, *gorm.DB, error) {
	db := r.writer()
	if db == nil {
		return nil, nil, errRepositoryUnavailable
	}
	return query.Bind(db, r.prefix()), db, nil
}

func (r *Repository) withDB(db *gorm.DB, inTx bool) *Repository {
	transactional := *r
	transactional.db = db
	transactional.inTx = inTx
	return &transactional
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
		return errors.New("invalid authentication transaction")
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
		return 0, errors.New("generated authentication ID must be positive")
	}
	return guid.ID(id), nil
}

func (r *Repository) appendAudit(ctx context.Context, event audit.DomainEvent) error {
	if r == nil || r.audit == nil {
		return errRepositoryUnavailable
	}
	db := r.writer()
	if db == nil {
		return errRepositoryUnavailable
	}
	if err := r.audit(ctx, db, event); err != nil {
		return fmt.Errorf("append authentication audit event: %w", err)
	}
	return nil
}
