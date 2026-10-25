package files

import (
	"context"
	"errors"
	"io"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

var ErrMigrationConflict = errors.New("files: object changed during migration")

type ObjectMigrator func(context.Context, guid.ID, string) (ObjectMigrationReport, error)

func (m *Module) provideObjectMigrator(services *module.Services) error {
	return module.Provide[ObjectMigrator](services, ObjectMigrator(m.MigrateObject))
}

// ObjectMigrationReport records an operational storage move, not a new file
// version. Source bytes are retained; this command never deletes user data.
type ObjectMigrationReport struct {
	ObjectID guid.ID `json:"object_id"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Size     int64   `json:"size"`
	SHA256   string  `json:"sha256"`
	Replayed bool    `json:"replayed"`
}

// MigrateObject is an operator-only maintenance capability. It is deliberately
// not exposed as an HTTP command: normal callers continue through current file
// IAM and bindings. I/O happens before the short version-CAS transaction.
func (m *Module) MigrateObject(ctx context.Context, id guid.ID, from string) (ObjectMigrationReport, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.accounts == nil || m.appender == nil || id <= 0 || from == "" || from == m.accounts.backend {
		return ObjectMigrationReport{}, ErrUnsupported
	}
	set, ok := m.driver.(*backendDriver)
	if !ok || set.drivers[from] == nil {
		return ObjectMigrationReport{}, ErrUnsupported
	}
	return migrateObject(ctx, m.runtime.Database().Write(), m.accounts.prefix, id, from, set.active, set.drivers[from], set.current(), m.appender)
}

func verifyMigrationBytes(ctx context.Context, driver Driver, row *model.Object) error {
	reader, err := driver.Open(ctx, row.StorageKey)
	if err != nil {
		return err
	}
	sums := NewChecksums()
	_, readErr := io.Copy(sums, contextReader{ctx, reader})
	err = errors.Join(readErr, reader.Close())
	if err != nil {
		return err
	}
	if sums.Offset() != row.Size || sums.Digests().SHA256 != row.SHA256 {
		return ErrLength
	}
	return nil
}

func migrateObject(ctx context.Context, db *gorm.DB, prefix string, id guid.ID, from, to string, source, target Driver, appendAudit audit.Appender) (ObjectMigrationReport, error) {
	var report ObjectMigrationReport
	if db == nil || id <= 0 || from == "" || to == "" || from == to || source == nil || target == nil || appendAudit == nil {
		return report, ErrUnsupported
	}
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok {
		return report, ErrAccountTransaction
	}
	q := query.Bind(db, prefix)
	row, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(id), q.Object.State.Eq("verified")).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return report, ErrNotFound
	}
	if err != nil {
		return report, err
	}
	report = ObjectMigrationReport{ObjectID: id, From: from, To: to, Size: row.Size, SHA256: row.SHA256}
	if len(row.SHA256) != 64 || !validKey(row.StorageKey) {
		return report, ErrLength
	}
	if row.BackendID == to {
		if err := verifyMigrationBytes(ctx, target, row); err != nil {
			return report, err
		}
		report.Replayed = true
		return report, nil
	}
	if row.BackendID != from {
		return report, ErrMigrationConflict
	}
	// Check the entire source before any write; then hash it again while copying
	// so source corruption between reads cannot publish unverified metadata.
	if err := verifyMigrationBytes(ctx, source, row); err != nil {
		return report, err
	}
	reader, err := source.Open(ctx, row.StorageKey)
	if err != nil {
		return report, err
	}
	sums := NewChecksums()
	uri, writeErr := target.Put(ctx, row.StorageKey, io.TeeReader(contextReader{ctx, reader}, sums), row.Size)
	closeErr := reader.Close()
	if writeErr != nil && !errors.Is(writeErr, ErrExists) {
		return report, errors.Join(writeErr, closeErr)
	}
	if closeErr != nil {
		return report, closeErr
	}
	if writeErr == nil && (sums.Offset() != row.Size || sums.Digests().SHA256 != row.SHA256) {
		return report, ErrLength
	}
	// An existing destination can be a previous interrupted copy or a collision.
	// Both must pass a real full destination read before the CAS is attempted.
	if err := verifyMigrationBytes(ctx, target, row); err != nil {
		return report, err
	}
	if uri == "" {
		uri = "storage://" + to + "/" + row.StorageKey
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.Bind(tx, prefix)
		changed, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(id), q.Object.BackendID.Eq(from), q.Object.StorageKey.Eq(row.StorageKey), q.Object.State.Eq("verified"), q.Object.Version.Eq(row.Version)).UpdateSimple(q.Object.BackendID.Value(to), q.Object.URI.Value(uri), q.Object.Version.Add(1))
		if err != nil {
			return err
		}
		if changed.RowsAffected != 1 {
			return ErrMigrationConflict
		}
		if _, err := q.AccountPreparation.WithContext(ctx).Where(q.AccountPreparation.ID.Eq(id), q.AccountPreparation.BackendID.Eq(from), q.AccountPreparation.State.Eq("attached")).Update(q.AccountPreparation.BackendID, to); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, 0, "file_object_migrated", "file_object", id)
		event.Detail["from_backend"], event.Detail["to_backend"], event.Detail["size"], event.Detail["sha256"] = from, to, row.Size, row.SHA256
		return appendAudit(ctx, tx, event)
	})
	return report, err
}
