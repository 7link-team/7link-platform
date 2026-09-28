package authn

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
)

var errRepositoryUnavailable = errors.New("authentication repository unavailable")

// Repository persists authentication state through the runtime's current
// writer. A transactional clone pins one *gorm.DB for the complete workflow.
type repositoryTables struct{ names query.TableNames }

type Repository struct {
	runtime *appenv.Runtime
	ids     guid.Source
	audit   audit.Appender
	db      *gorm.DB
	inTx    bool
	// repositoryTables holds the physical names of every table authn owns under
	// this deployment's prefix, resolved once at construction so every
	// statement the repository runs spells them the same way. It is embedded
	// unexported so the exported Repository carries no storage type.
	repositoryTables
	// foreign holds the names of the tables this repository reads but does not
	// own. The module's wiring attaches the holder its connectors bind; a
	// repository built without one reports the published tables as missing
	// instead of reading a table nobody named.
	foreign *foreignTables
	// principals is identity's principal-state capability, used for the reads
	// and locks on identity's own table that authn does not have to JOIN with
	// one of its own.
	principals identity.PrincipalStateStore
}

func NewRepository(runtime *appenv.Runtime, ids guid.Source, appender audit.Appender) *Repository {
	if runtime == nil || ids == nil || appender == nil {
		panic("authentication repository requires runtime, id generator, and audit appender")
	}
	return &Repository{
		runtime:          runtime,
		ids:              ids,
		audit:            appender,
		repositoryTables: repositoryTables{names: query.BindNames(runtime.TablePrefix())},
		foreign:          &foreignTables{},
	}
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

// BindForeignTables records the table-name sets of the tables this repository
// reads but does not own. The modules that own them reach it through the
// connectors authn publishes and the receiver identity pushes into; a caller
// that builds a repository outside the module has to call it itself, because an
// unbound repository reports the publication as missing rather than running a
// statement against an empty table name.
func (r *Repository) BindForeignTables(
	directory DirectoryTableNames, oauth OAuthTableNames,
	access core.AccessTableNames, principal identity.PrincipalTableNames,
) error {
	if r == nil {
		return errRepositoryUnavailable
	}
	if r.foreign == nil {
		r.foreign = &foreignTables{}
	}
	return r.foreign.bind(directory, oauth, access, principal)
}

// BindPrincipalState records identity's principal-state capability, which is how
// this repository reads and locks identity's own table. The module receives it
// through the receiver it provides and hands it on in Initialize; a caller that
// builds a repository outside the module has to supply it, because an unwired
// repository reports the capability as missing rather than reading nothing and
// calling it an absence.
func (r *Repository) BindPrincipalState(store identity.PrincipalStateStore) error {
	if r == nil {
		return errRepositoryUnavailable
	}
	r.principals = store
	return nil
}

func (r *Repository) tables() (*query.Tables, *gorm.DB, error) {
	db := r.writer()
	if db == nil {
		return nil, nil, errRepositoryUnavailable
	}
	return query.BindWithNames(db, r.names), db, nil
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

// assignPublicID gives a session or passkey row its snowflake API identity
// before it is inserted, unless the caller already chose one.
func (r *Repository) assignPublicID(target **guid.ID) error {
	if *target != nil {
		return nil
	}
	id, err := r.nextID()
	if err != nil {
		return err
	}
	*target = &id
	return nil
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
