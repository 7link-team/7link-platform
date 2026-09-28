package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

// ErrAccountTransaction rejects an unsafe account/storage transaction boundary.
var ErrAccountTransaction = errors.New("files: invalid account transaction boundary")

// AccountObjects owns persistence of private account files. Storage preparation
// runs before the caller's transaction; registration uses that same transaction.
type AccountObjects struct {
	driver   Driver
	backend  string
	prefix   string
	metadata *MetadataRegistry
}

// Adopt registers immutable bytes that already exist in the configured
// backend. It is used only by bounded legacy migration and never rewrites the
// object. The returned preparation follows the same transactional attach path
// as a newly staged upload.
func (a *AccountObjects) Adopt(ctx context.Context, db *gorm.DB, id, principalID guid.ID, key string, deadline time.Time) (*PreparedAccountObject, error) {
	if a == nil || db == nil || id <= 0 || principalID <= 0 || !validKey(key) || !deadline.After(time.Now()) {
		return nil, ErrInvalidKey
	}
	if _, active := db.Statement.ConnPool.(gorm.TxCommitter); active {
		return nil, ErrAccountTransaction
	}
	reader, err := a.driver.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	writer := &prefixChecksums{checksums: NewChecksums()}
	size, readErr := io.Copy(writer, reader)
	if err := errors.Join(readErr, reader.Close()); err != nil {
		return nil, err
	}
	meta := a.metadata.Detect(writer.prefix)
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	digests := writer.checksums.Digests()
	now := time.Now()
	prepared := &PreparedAccountObject{owner: a, staged: true, object: model.Object{
		ID: id, BackendID: a.backend, StorageKey: key, URI: fmt.Sprintf("storage://%s/%s", a.backend, key),
		State: "staged", Size: size, MD5: digests.MD5, SHA1: digests.SHA1, SHA256: digests.SHA256, SHA512: digests.SHA512,
		MIME: meta.MIME, Type: meta.Type, Subtype: meta.Subtype, Format: meta.Format,
		Width: meta.Width, Height: meta.Height, MetadataJSON: string(raw), MetadataState: meta.Status,
		ExtractorVersion: meta.ExtractorVersion, Version: 1, CreatedAt: now.UnixMilli(),
	}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.Bind(tx, a.prefix)
		intent := model.AccountPreparation{
			ID: id, PrincipalID: principalID, BackendID: a.backend, StorageKey: key,
			State: "ready", ExpiresAt: deadline.UnixMilli(), CreatedAt: now.UnixMilli(),
		}
		if err := q.AccountPreparation.WithContext(ctx).Create(&intent); err != nil {
			return err
		}
		return q.Object.WithContext(ctx).Create(&prepared.object)
	})
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

// NewAccountObjects binds private resources to one configured backend and schema.
func NewAccountObjects(driver Driver, backend, prefix string) (*AccountObjects, error) {
	if driver == nil || backend == "" || len(backend) > 64 {
		return nil, ErrUnsupported
	}
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	metadata := NewMetadataRegistry()
	_ = metadata.Register(ImageMetadata{})
	return &AccountObjects{driver: driver, backend: backend, prefix: prefix, metadata: metadata}, nil
}

// PreparedAccountObject contains a completed immutable storage write. Its fields
// stay private so callers cannot register unverified client-supplied digests.
type PreparedAccountObject struct {
	owner  *AccountObjects
	object model.Object
	staged bool
}

type prefixChecksums struct {
	checksums *Checksums
	prefix    []byte
}

func (w *prefixChecksums) Write(p []byte) (int, error) {
	if remaining := MetadataPrefixLimit - len(w.prefix); remaining > 0 {
		w.prefix = append(w.prefix, p[:min(remaining, len(p))]...)
	}
	return w.checksums.Write(p)
}

// Prepare streams once into final storage, computing all hashes and retaining
// only a bounded metadata prefix. No database transaction is opened here.
func (a *AccountObjects) Prepare(ctx context.Context, id guid.ID, key string, source io.Reader, size int64, now time.Time) (*PreparedAccountObject, error) {
	if id <= 0 || !validKey(key) || source == nil || size < 0 {
		return nil, ErrInvalidKey
	}
	writer := &prefixChecksums{checksums: NewChecksums()}
	uri, err := a.driver.Put(ctx, key, io.TeeReader(source, writer), size)
	if err != nil {
		return nil, err
	}
	if writer.checksums.Offset() != size {
		return nil, errors.Join(ErrLength, a.driver.Delete(ctx, key))
	}
	meta := a.metadata.Detect(writer.prefix)
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, errors.Join(err, a.driver.Delete(ctx, key))
	}
	digests := writer.checksums.Digests()
	return &PreparedAccountObject{owner: a, object: model.Object{
		ID: id, BackendID: a.backend, StorageKey: key, URI: uri, State: "verified", Size: size,
		MD5: digests.MD5, SHA1: digests.SHA1, SHA256: digests.SHA256, SHA512: digests.SHA512,
		MIME: meta.MIME, Type: meta.Type, Subtype: meta.Subtype, Format: meta.Format,
		Width: meta.Width, Height: meta.Height, MetadataJSON: string(raw), MetadataState: meta.Status,
		ExtractorVersion: meta.ExtractorVersion, Version: 1, CreatedAt: now.UnixMilli(),
	}}, nil
}

// Register creates both rows using the supplied caller transaction. The caller
// must roll back that transaction if registration or its account update fails.
func (a *AccountObjects) Register(ctx context.Context, tx *gorm.DB, prepared *PreparedAccountObject, referenceID, principalID guid.ID, purpose string, expiresAt *time.Time) error {
	if tx == nil || prepared == nil || prepared.owner != a || referenceID <= 0 || principalID <= 0 || (purpose != "avatar" && purpose != "avatar_candidate") {
		return ErrInvalidKey
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrAccountTransaction
	}
	if (purpose == "avatar_candidate") != (expiresAt != nil) || (expiresAt != nil && expiresAt.UnixMilli() <= prepared.object.CreatedAt) {
		return ErrInvalidKey
	}
	q := query.Bind(tx, a.prefix)
	object := prepared.object
	if prepared.staged {
		intent := q.AccountPreparation
		result, err := intent.WithContext(ctx).Where(intent.ID.Eq(object.ID), intent.PrincipalID.Eq(principalID), intent.State.Eq("ready"), intent.ExpiresAt.Gt(time.Now().UnixMilli())).Update(intent.State, "attached")
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		result, err = q.Object.WithContext(ctx).Where(q.Object.ID.Eq(object.ID), q.Object.State.Eq("staged")).Update(q.Object.State, "verified")
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
	} else {
		if err := q.Object.WithContext(ctx).Create(&object); err != nil {
			return err
		}
	}
	reference := model.AccountReference{ID: referenceID, PrincipalID: principalID, ObjectID: object.ID, Purpose: purpose, State: "active", CreatedAt: object.CreatedAt}
	if expiresAt != nil {
		value := expiresAt.UnixMilli()
		reference.ExpiresAt = &value
	}
	return q.AccountReference.WithContext(ctx).Create(&reference)
}

// Stage reserves the storage key durably before preparing content. It must run
// outside the account transaction, using its caller's pinned current writer.
func (a *AccountObjects) Stage(ctx context.Context, db *gorm.DB, id, principalID guid.ID, key string, source io.Reader, size int64, deadline time.Time) (*PreparedAccountObject, error) {
	if db == nil {
		return nil, ErrUnsupported
	}
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok {
		return nil, ErrAccountTransaction
	}
	now := time.Now()
	if id <= 0 || principalID <= 0 || !validKey(key) || source == nil || size < 0 || !deadline.After(now) {
		return nil, ErrInvalidKey
	}
	q := query.Bind(db, a.prefix)
	intent := model.AccountPreparation{ID: id, PrincipalID: principalID, BackendID: a.backend, StorageKey: key, State: "writing", ExpiresAt: deadline.UnixMilli(), CreatedAt: now.UnixMilli()}
	if err := q.AccountPreparation.WithContext(ctx).Create(&intent); err != nil {
		return nil, err
	}
	prepared, err := a.Prepare(ctx, id, key, source, size, now)
	if err != nil {
		// A confirmed preexisting immutable key is NOT ours to delete.
		state := "abandoned"
		if errors.Is(err, ErrExists) {
			state = "conflict"
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, updateErr := q.AccountPreparation.WithContext(cleanupContext).Where(q.AccountPreparation.ID.Eq(id), q.AccountPreparation.State.In("writing", "abandoned")).Update(q.AccountPreparation.State, state)
		return nil, errors.Join(err, updateErr)
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.Bind(tx, a.prefix)
		intent := q.AccountPreparation
		result, err := intent.WithContext(ctx).Where(intent.ID.Eq(id), intent.State.Eq("writing"), intent.ExpiresAt.Gt(time.Now().UnixMilli())).Update(intent.State, "ready")
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		object := prepared.object
		object.State = "staged"
		return q.Object.WithContext(ctx).Create(&object)
	})
	if err != nil {
		// Leave the durable intent for reconciliation, including an ambiguous
		// commit. Never delete here: another observer may have attached it.
		return nil, err
	}
	prepared.staged = true
	return prepared, nil
}

// attachFileObject promotes a staged immutable object inside the transaction
// that creates its first file version. A rollback leaves the preparation ready
// for the normal expiry worker instead of exposing an unreferenced object.
func (a *AccountObjects) attachFileObject(ctx context.Context, tx *gorm.DB, prepared *PreparedAccountObject, principalID guid.ID) (*model.Object, error) {
	if a == nil || tx == nil || prepared == nil || prepared.owner != a || !prepared.staged || principalID <= 0 {
		return nil, ErrInvalidKey
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return nil, ErrAccountTransaction
	}
	q := query.Bind(tx, a.prefix)
	intent := q.AccountPreparation
	result, err := intent.WithContext(ctx).Where(
		intent.ID.Eq(prepared.object.ID), intent.PrincipalID.Eq(principalID), intent.State.Eq("ready"), intent.ExpiresAt.Gt(time.Now().UnixMilli()),
	).Update(intent.State, "attached")
	if err != nil {
		return nil, err
	}
	if result.RowsAffected != 1 {
		return nil, ErrNotFound
	}
	object := q.Object
	result, err = object.WithContext(ctx).Where(object.ID.Eq(prepared.object.ID), object.State.Eq("staged")).Update(object.State, "verified")
	if err != nil {
		return nil, err
	}
	if result.RowsAffected != 1 {
		return nil, ErrNotFound
	}
	attached := prepared.object
	attached.State = "verified"
	return &attached, nil
}

// UseAvatarCandidate promotes an authorized unexpired candidate without copying
// bytes. The object row serializes reference creation against cleanup claims.
func (a *AccountObjects) UseAvatarCandidate(ctx context.Context, tx *gorm.DB, principalID, referenceID guid.ID, key string, now time.Time) error {
	if tx == nil || principalID <= 0 || referenceID <= 0 || !validKey(key) {
		return ErrInvalidKey
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrAccountTransaction
	}
	q := query.Bind(tx, a.prefix)
	object := q.Object
	row, err := object.WithContext(ctx).Where(object.BackendID.Eq(a.backend), object.StorageKey.Eq(key), object.State.Eq("verified")).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	result, err := object.WithContext(ctx).Where(object.ID.Eq(row.ID), object.State.Eq("verified")).UpdateSimple(object.Version.Add(1))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	ref := q.AccountReference
	candidate, err := ref.WithContext(ctx).Where(ref.PrincipalID.Eq(principalID), ref.ObjectID.Eq(row.ID), ref.Purpose.Eq("avatar_candidate"), ref.State.Eq("active"), ref.ExpiresAt.Gt(now.UnixMilli())).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	result, err = ref.WithContext(ctx).Where(ref.ID.Eq(candidate.ID), ref.State.Eq("active")).Update(ref.State, "inactive")
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	if _, err := ref.WithContext(ctx).Where(ref.PrincipalID.Eq(principalID), ref.Purpose.Eq("avatar"), ref.State.Eq("active")).Update(ref.State, "inactive"); err != nil {
		return err
	}
	return ref.WithContext(ctx).Create(&model.AccountReference{ID: referenceID, PrincipalID: principalID, ObjectID: row.ID, Purpose: "avatar", State: "active", CreatedAt: now.UnixMilli()})
}

// ReplaceAvatar deactivates previous references in the account transaction.
// The physical bytes are reclaimed asynchronously, never before commit.
func (a *AccountObjects) ReplaceAvatar(ctx context.Context, tx *gorm.DB, prepared *PreparedAccountObject, referenceID, principalID guid.ID) error {
	if err := a.Register(ctx, tx, prepared, referenceID, principalID, "avatar", nil); err != nil {
		return err
	}
	q := query.Bind(tx, a.prefix)
	ref := q.AccountReference
	_, err := ref.WithContext(ctx).Where(ref.PrincipalID.Eq(principalID), ref.Purpose.Eq("avatar"), ref.State.Eq("active"), ref.ID.Neq(referenceID)).Update(ref.State, "inactive")
	return err
}

// AccountKeyReadable checks live private references, not merely a storage key.
func (a *AccountObjects) AccountKeyReadable(ctx context.Context, db *gorm.DB, principalID guid.ID, key string, now time.Time) (bool, error) {
	if db == nil || principalID <= 0 || !validKey(key) {
		return false, ErrInvalidKey
	}
	q := query.Bind(db, a.prefix)
	object := q.Object
	row, err := object.WithContext(ctx).Where(object.BackendID.Eq(a.backend), object.StorageKey.Eq(key), object.State.Eq("verified")).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ref := q.AccountReference
	count, err := ref.WithContext(ctx).Where(ref.PrincipalID.Eq(principalID), ref.ObjectID.Eq(row.ID), ref.State.Eq("active")).Where(ref.WithContext(ctx).Where(ref.ExpiresAt.IsNull()).Or(ref.ExpiresAt.Gt(now.UnixMilli()))).Count()
	return count > 0, err
}

// ReleaseAccountResources revokes account file references in the caller's
// erasure transaction. Physical reclamation happens only after that commits.
func (a *AccountObjects) ReleaseAccountResources(ctx context.Context, tx *gorm.DB, principalID guid.ID, now time.Time) error {
	if tx == nil || principalID <= 0 {
		return ErrInvalidKey
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrAccountTransaction
	}
	q := query.Bind(tx, a.prefix)
	ref := q.AccountReference
	if _, err := ref.WithContext(ctx).Where(ref.PrincipalID.Eq(principalID), ref.State.Eq("active")).Update(ref.State, "inactive"); err != nil {
		return err
	}
	intent := q.AccountPreparation
	_, err := intent.WithContext(ctx).Where(intent.PrincipalID.Eq(principalID), intent.State.In("writing", "ready")).Update(intent.ExpiresAt, now.UnixMilli())
	return err
}
