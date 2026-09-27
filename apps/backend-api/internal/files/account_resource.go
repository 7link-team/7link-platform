package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

const (
	PrincipalAvatarPurpose          = "principal.avatar"
	PrincipalAvatarCandidatePurpose = "principal.avatar-candidate"
)

// AccountResourceFiles is the narrow business-file capability for resources
// that belong to one global account. It keeps storage keys and tree internals
// inside the files module.
type AccountResourceFiles struct {
	accounts *AccountObjects
	bindings *ResourceBindings
	driver   Driver
	prefix   string
	ids      guid.Source
	appender audit.Appender
}

type AccountResourceInput struct {
	Prepared    *PreparedAccountObject
	PrincipalID guid.ID
	Purpose     string
	Slot        string
	Name        string
	ExpiresAt   *time.Time
	ActorID     guid.ID
	Replace     bool
}

type AccountResourceRead struct {
	FileID guid.ID
	Name   string
	MIME   string
	Size   int64
	Reader io.ReadCloser
}

func NewAccountResourceFiles(accounts *AccountObjects, bindings *ResourceBindings, driver Driver, prefix string) (*AccountResourceFiles, error) {
	if accounts == nil || bindings == nil || driver == nil {
		return nil, ErrUnsupported
	}
	if _, err := NewResourceBindings(prefix); err != nil {
		return nil, err
	}
	return &AccountResourceFiles{accounts: accounts, bindings: bindings, driver: driver, prefix: prefix}, nil
}

func (r *AccountResourceFiles) Configure(ids guid.Source, appender audit.Appender) error {
	if r == nil || ids == nil || appender == nil {
		return ErrUnsupported
	}
	r.ids, r.appender = ids, appender
	return nil
}

func (r *AccountResourceFiles) RegisterPurpose(purpose ResourcePurpose) error {
	if r == nil || r.bindings == nil {
		return ErrUnsupported
	}
	return r.bindings.RegisterPurpose(purpose)
}

func (r *AccountResourceFiles) nextID() (guid.ID, error) {
	if r == nil || r.ids == nil {
		return 0, ErrUnsupported
	}
	id, err := r.ids.Next()
	if err != nil || id <= 0 {
		return 0, errors.Join(ErrUnsupported, err)
	}
	return guid.ID(id), nil
}

// StageOn stages bytes through the current runtime writer without exposing the
// generated object identity to the business module.
func (r *AccountResourceFiles) StageOn(ctx context.Context, db *gorm.DB, principalID guid.ID, key string, source io.Reader, size int64, deadline time.Time) (*PreparedAccountObject, error) {
	if r == nil || r.accounts == nil {
		return nil, ErrUnsupported
	}
	id, err := r.nextID()
	if err != nil {
		return nil, err
	}
	return r.accounts.Stage(ctx, db, id, principalID, key, source, size, deadline)
}

// AdoptOn brings an existing immutable legacy object under the same durable
// preparation and attachment lifecycle without copying its bytes.
func (r *AccountResourceFiles) AdoptOn(ctx context.Context, db *gorm.DB, principalID guid.ID, key string, deadline time.Time) (*PreparedAccountObject, error) {
	if r == nil || r.accounts == nil {
		return nil, ErrUnsupported
	}
	id, err := r.nextID()
	if err != nil {
		return nil, err
	}
	return r.accounts.Adopt(ctx, db, id, principalID, key, deadline)
}

func (r *AccountResourceFiles) Create(ctx context.Context, tx *gorm.DB, input AccountResourceInput, now time.Time) (*model.ResourceBinding, error) {
	if input.Prepared == nil {
		return nil, ErrInvalidKey
	}
	return r.create(ctx, tx, input, 0, now)
}

func (r *AccountResourceFiles) createExisting(ctx context.Context, tx *gorm.DB, input AccountResourceInput, objectID guid.ID, now time.Time) (*model.ResourceBinding, error) {
	if input.Prepared != nil || objectID <= 0 {
		return nil, ErrInvalidKey
	}
	return r.create(ctx, tx, input, objectID, now)
}

func (r *AccountResourceFiles) create(ctx context.Context, tx *gorm.DB, input AccountResourceInput, objectID guid.ID, now time.Time) (*model.ResourceBinding, error) {
	if r == nil || r.accounts == nil || r.bindings == nil || tx == nil || input.PrincipalID <= 0 || input.ActorID <= 0 || input.Name == "" || (input.Prepared == nil) == (objectID <= 0) {
		return nil, ErrInvalidKey
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return nil, ErrAccountTransaction
	}
	space, root, err := r.ensureRoot(ctx, tx, input.PrincipalID, input.ActorID, now)
	if err != nil {
		return nil, err
	}
	fileID, err := r.nextID()
	if err != nil {
		return nil, err
	}
	versionID, err := r.nextID()
	if err != nil {
		return nil, err
	}
	bindingID, err := r.nextID()
	if err != nil {
		return nil, err
	}
	tree := directoryTree{prefix: r.prefix}
	file, err := tree.createNodeOn(ctx, tx, space.ID, fileID, root.ID, "file", input.Name, now)
	if err != nil {
		return nil, err
	}
	if input.Prepared != nil {
		object, attachErr := r.accounts.attachFileObject(ctx, tx, input.Prepared, input.PrincipalID)
		if attachErr != nil {
			return nil, attachErr
		}
		objectID = object.ID
	}
	if err := tree.appendVersion(ctx, tx, model.Version{
		ID: versionID, SpaceID: space.ID, NodeID: file.ID, Number: 1, ObjectID: objectID,
		Ext: file.Ext, DeclaredMIME: "image/png", CreatedBy: input.ActorID,
	}, file.Version, now); err != nil {
		return nil, err
	}
	q := query.Bind(tx, r.prefix)
	file, err = q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(space.ID), q.Node.ID.Eq(file.ID)).Take()
	if err != nil {
		return nil, err
	}
	if input.Replace {
		if err := r.deactivateSlot(ctx, tx, input.PrincipalID, input.Purpose, input.Slot, now); err != nil {
			return nil, err
		}
	}
	binding, err := r.bindings.Bind(ctx, tx, ResourceBindingInput{
		ID: bindingID, SpaceID: space.ID, FileID: file.ID, ExpectedFileVersion: file.Version,
		Purpose: input.Purpose, ResourceKind: "principal", ResourceID: input.PrincipalID,
		Slot: input.Slot, ExpiresAt: input.ExpiresAt, ActorID: input.ActorID,
	}, now)
	if err != nil {
		return nil, err
	}
	if err := r.appendAudit(ctx, tx, input.ActorID, "file_resource_bound", file.ID, map[string]any{
		"purpose": input.Purpose, "resource_id": input.PrincipalID, "slot": input.Slot,
	}); err != nil {
		return nil, err
	}
	return binding, nil
}

func (r *AccountResourceFiles) ensureRoot(ctx context.Context, tx *gorm.DB, principalID, actorID guid.ID, now time.Time) (*model.Space, *model.Node, error) {
	tree := directoryTree{prefix: r.prefix}
	q := query.Bind(tx, r.prefix)
	space, err := q.Space.WithContext(ctx).Where(q.Space.ScopeKind.Eq(spaceAccount), q.Space.PrincipalID.Eq(principalID)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		spaceID, idErr := r.nextID()
		if idErr != nil {
			return nil, nil, idErr
		}
		rootID, idErr := r.nextID()
		if idErr != nil {
			return nil, nil, idErr
		}
		if err := tree.createSpaceOn(ctx, tx, spaceID, rootID, principalID, spaceAccount, now); err != nil {
			return nil, nil, err
		}
		space, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).Take()
	}
	if err != nil {
		return nil, nil, err
	}
	if space.ScopeKind != spaceAccount || space.PrincipalID == nil || *space.PrincipalID != principalID {
		return nil, nil, errRootUnbound
	}
	root, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(space.ID), q.Node.ParentID.IsNull(), q.Node.RootSlot.Eq(1), q.Node.State.Eq("active")).Take()
	if err != nil {
		return nil, nil, err
	}
	if root.RootScope == nil {
		if err := tree.bindRoot(ctx, tx, space.ID, nodeRevision{ID: root.ID, Version: root.Version}, space.Version, rootBinding{scope: ownerAccount, ownerID: principalID}, now); err != nil {
			return nil, nil, err
		}
		root, err = q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(space.ID), q.Node.ID.Eq(root.ID)).Take()
		if err != nil {
			return nil, nil, err
		}
		space, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(space.ID)).Take()
		if err != nil {
			return nil, nil, err
		}
		rules := []nodeACLRule{{
			subject: aclSubject{kind: "owner", id: principalID}, effect: "allow",
			actions: []string{"list", "traverse", "read", "download", "create", "update", "rename", "move", "delete", "restore", "purge", "share", "manage_acl"},
			applies: "self_and_descendants",
		}}
		if err := tree.replaceNodeACL(ctx, tx, space.ID, nodeRevision{ID: root.ID, Version: root.Version}, space.Version, true, actorID, rules, now, r.ids.Next); err != nil {
			return nil, nil, err
		}
		root, err = q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(space.ID), q.Node.ID.Eq(root.ID)).Take()
		if err != nil {
			return nil, nil, err
		}
		if err := r.appendAudit(ctx, tx, actorID, "file_root_created", root.ID, map[string]any{"scope": ownerAccount}); err != nil {
			return nil, nil, err
		}
	}
	if root.RootScope == nil || *root.RootScope != ownerAccount || root.RootOwnerID == nil || *root.RootOwnerID != principalID {
		return nil, nil, errRootUnbound
	}
	return space, root, nil
}

func (r *AccountResourceFiles) deactivateSlot(ctx context.Context, tx *gorm.DB, principalID guid.ID, purpose, slot string, now time.Time) error {
	q := query.Bind(tx, r.prefix)
	binding := q.ResourceBinding
	rows, err := binding.WithContext(ctx).Where(
		binding.ResourceKind.Eq("principal"), binding.ResourceID.Eq(principalID), binding.Purpose.Eq(purpose),
		binding.Slot.Eq(slot), binding.State.Eq("active"),
	).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := r.bindings.Unbind(ctx, tx, row.SpaceID, row.ID, row.Version, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *AccountResourceFiles) Promote(ctx context.Context, tx *gorm.DB, principalID guid.ID, candidateSlot, targetPurpose, targetSlot string, actorID guid.ID, now time.Time) error {
	if r == nil || tx == nil || principalID <= 0 || actorID <= 0 {
		return ErrInvalidKey
	}
	q := query.Bind(tx, r.prefix)
	binding := q.ResourceBinding
	candidate, err := binding.WithContext(ctx).Where(
		binding.ResourceKind.Eq("principal"), binding.ResourceID.Eq(principalID),
		binding.Purpose.Eq(PrincipalAvatarCandidatePurpose), binding.Slot.Eq(candidateSlot),
		binding.State.Eq("active"), binding.ExpiresAt.Gt(now.UnixMilli()),
	).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	tree := directoryTree{prefix: r.prefix}
	q, _, err = tree.lockOperationSpace(ctx, tx, candidate.SpaceID)
	if err != nil {
		return err
	}
	space, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(candidate.SpaceID), q.Space.ScopeKind.Eq(spaceAccount), q.Space.PrincipalID.Eq(principalID)).Take()
	if err != nil || space == nil {
		return ErrNotFound
	}
	file, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(candidate.SpaceID), q.Node.ID.Eq(candidate.FileID), q.Node.State.Eq("active"), q.Node.ManagementMode.Eq("resource")).Take()
	if err != nil {
		return ErrNotFound
	}
	if err := r.deactivateSlot(ctx, tx, principalID, targetPurpose, targetSlot, now); err != nil {
		return err
	}
	if err := r.bindings.Unbind(ctx, tx, candidate.SpaceID, candidate.ID, candidate.Version, now); err != nil {
		return err
	}
	bindingID, err := r.nextID()
	if err != nil {
		return err
	}
	_, err = r.bindings.Bind(ctx, tx, ResourceBindingInput{
		ID: bindingID, SpaceID: candidate.SpaceID, FileID: candidate.FileID, ExpectedFileVersion: file.Version,
		Purpose: targetPurpose, ResourceKind: "principal", ResourceID: principalID, Slot: targetSlot, ActorID: actorID,
	}, now)
	return err
}

func (r *AccountResourceFiles) Open(ctx context.Context, db *gorm.DB, principalID guid.ID, purpose, slot string, now time.Time) (*AccountResourceRead, error) {
	if r == nil || db == nil || r.driver == nil || principalID <= 0 {
		return nil, ErrInvalidKey
	}
	q := query.Bind(db, r.prefix)
	binding := q.ResourceBinding
	row, err := binding.WithContext(ctx).Where(
		binding.ResourceKind.Eq("principal"), binding.ResourceID.Eq(principalID), binding.Purpose.Eq(purpose),
		binding.Slot.Eq(slot), binding.State.Eq("active"),
	).Where(binding.WithContext(ctx).Where(binding.ExpiresAt.IsNull()).Or(binding.ExpiresAt.Gt(now.UnixMilli()))).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(row.SpaceID), q.Space.ScopeKind.Eq(spaceAccount), q.Space.PrincipalID.Eq(principalID)).Take(); err != nil {
		return nil, ErrNotFound
	}
	file, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(row.SpaceID), q.Node.ID.Eq(row.FileID), q.Node.State.Eq("active"), q.Node.ManagementMode.Eq("resource")).Take()
	if err != nil || file.CurrentVersionID == nil {
		return nil, ErrNotFound
	}
	versionID := file.CurrentVersionID
	if row.VersionID != nil {
		versionID = row.VersionID
	}
	version, err := q.Version.WithContext(ctx).Where(q.Version.SpaceID.Eq(row.SpaceID), q.Version.NodeID.Eq(row.FileID), q.Version.ID.Eq(*versionID)).Take()
	if err != nil {
		return nil, ErrNotFound
	}
	object, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(version.ObjectID), q.Object.State.Eq("verified")).Take()
	if err != nil {
		return nil, ErrNotFound
	}
	reader, err := r.driver.Open(ctx, object.StorageKey)
	if err != nil {
		return nil, err
	}
	return &AccountResourceRead{FileID: file.ID, Name: file.Name, MIME: object.MIME, Size: object.Size, Reader: reader}, nil
}

func (r *AccountResourceFiles) Has(ctx context.Context, db *gorm.DB, principalID guid.ID, purpose, slot string, now time.Time) (bool, error) {
	if r == nil || db == nil || principalID <= 0 {
		return false, ErrInvalidKey
	}
	q := query.Bind(db, r.prefix)
	binding := q.ResourceBinding
	count, err := binding.WithContext(ctx).Where(
		binding.ResourceKind.Eq("principal"), binding.ResourceID.Eq(principalID), binding.Purpose.Eq(purpose),
		binding.Slot.Eq(slot), binding.State.Eq("active"),
	).Where(binding.WithContext(ctx).Where(binding.ExpiresAt.IsNull()).Or(binding.ExpiresAt.Gt(now.UnixMilli()))).Count()
	return count > 0, err
}

func (r *AccountResourceFiles) Release(ctx context.Context, tx *gorm.DB, principalID guid.ID, now time.Time) error {
	if r == nil || tx == nil || principalID <= 0 {
		return ErrInvalidKey
	}
	q := query.Bind(tx, r.prefix)
	binding := q.ResourceBinding
	rows, err := binding.WithContext(ctx).Where(binding.ResourceKind.Eq("principal"), binding.ResourceID.Eq(principalID), binding.State.Eq("active")).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := r.bindings.Unbind(ctx, tx, row.SpaceID, row.ID, row.Version, now); err != nil {
			return err
		}
	}
	return r.accounts.ReleaseAccountResources(ctx, tx, principalID, now)
}

func (r *AccountResourceFiles) Expire(ctx context.Context, db *gorm.DB, now time.Time, after guid.ID, limit int) (guid.ID, error) {
	if r == nil || r.bindings == nil {
		return after, ErrUnsupported
	}
	return r.bindings.Expire(ctx, db, now, after, limit)
}

// PurgeOrphaned removes one bounded page of hidden resource-managed logical
// files after their final active binding has gone away. It never removes a
// node pinned by an in-flight file operation. Object bytes remain governed by
// the existing preparation/object collector and are reclaimed only afterward.
func (r *AccountResourceFiles) PurgeOrphaned(ctx context.Context, db *gorm.DB, now time.Time, after guid.ID, limit int) (guid.ID, error) {
	if r == nil || db == nil || after < 0 || limit < 1 || limit > 1000 {
		return after, ErrInvalidKey
	}
	rows, err := r.bindings.OrphanedFiles(ctx, db, after, limit)
	if err != nil {
		return after, err
	}
	var errs []error
	for _, candidate := range rows {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			tree := directoryTree{prefix: r.prefix}
			q, _, err := tree.lockOperationSpace(ctx, tx, candidate.SpaceID)
			if err != nil {
				return err
			}
			node, err := q.Node.WithContext(ctx).Where(
				q.Node.SpaceID.Eq(candidate.SpaceID), q.Node.ID.Eq(candidate.ID), q.Node.Kind.Eq("file"),
				q.Node.State.Eq("active"), q.Node.ManagementMode.Eq("resource"),
			).Take()
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			active, err := q.ResourceBinding.WithContext(ctx).Where(
				q.ResourceBinding.SpaceID.Eq(node.SpaceID), q.ResourceBinding.FileID.Eq(node.ID), q.ResourceBinding.State.Eq("active"),
			).Count()
			if err != nil || active != 0 {
				return err
			}
			pins, err := q.OperationItem.WithContext(ctx).Where(
				q.OperationItem.SpaceID.Eq(node.SpaceID), q.OperationItem.PinnedNodeID.Eq(node.ID),
			).Count()
			if err != nil || pins != 0 {
				return err
			}
			history, err := q.ResourceBinding.WithContext(ctx).Where(
				q.ResourceBinding.SpaceID.Eq(node.SpaceID), q.ResourceBinding.FileID.Eq(node.ID),
			).Order(q.ResourceBinding.ID.Desc()).First()
			if err != nil {
				return err
			}
			if _, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(node.SpaceID), q.Node.ID.Eq(node.ID), q.Node.Version.Eq(node.Version)).UpdateSimple(q.Node.CurrentVersionID.Null()); err != nil {
				return err
			}
			if _, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.SpaceID.Eq(node.SpaceID), q.ResourceBinding.FileID.Eq(node.ID)).Delete(); err != nil {
				return err
			}
			if _, err := q.NodeACL.WithContext(ctx).Where(q.NodeACL.SpaceID.Eq(node.SpaceID), q.NodeACL.NodeID.Eq(node.ID)).Delete(); err != nil {
				return err
			}
			if _, err := q.Version.WithContext(ctx).Where(q.Version.SpaceID.Eq(node.SpaceID), q.Version.NodeID.Eq(node.ID)).Delete(); err != nil {
				return err
			}
			closure := q.NodeClosure
			if _, err := closure.WithContext(ctx).Where(field.Or(closure.AncestorID.Eq(node.ID), closure.DescendantID.Eq(node.ID))).Delete(); err != nil {
				return err
			}
			result, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(node.SpaceID), q.Node.ID.Eq(node.ID)).Delete()
			if err != nil {
				return err
			}
			if result.RowsAffected != 1 {
				return errResourceBindingConflict
			}
			if _, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(node.SpaceID)).UpdateSimple(q.Space.Version.Add(1)); err != nil {
				return err
			}
			return r.appendAudit(ctx, tx, history.CreatedBy, "file_resource_purged", node.ID, map[string]any{"purpose": history.Purpose})
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(rows) < limit {
		return 0, errors.Join(errs...)
	}
	return rows[len(rows)-1].ID, errors.Join(errs...)
}

func (r *AccountResourceFiles) appendAudit(ctx context.Context, tx *gorm.DB, actorID guid.ID, eventType string, fileID guid.ID, detail map[string]any) error {
	if r.appender == nil {
		return ErrUnsupported
	}
	event := audit.NewDomainEvent(ctx, 0, eventType, "file_node", fileID)
	if event.PrincipalID.Zero() {
		event.PrincipalID = actorID
	}
	for key, value := range detail {
		event.Detail[key] = value
	}
	return r.appender(ctx, tx, event)
}

func candidateSlotFromKey(key string) (string, error) {
	base := path.Base(key)
	if base == "." || base == "/" || !strings.HasSuffix(base, ".png") {
		return "", ErrInvalidKey
	}
	token := strings.TrimSuffix(base, ".png")
	if len(token) != 48 {
		return "", ErrInvalidKey
	}
	return "candidate-" + token, nil
}

func (r *AccountResourceFiles) MigrateLegacyAvatars(ctx context.Context, db *gorm.DB, now time.Time) error {
	if r == nil || db == nil {
		return ErrUnsupported
	}
	q := query.Bind(db, r.prefix)
	ref := q.AccountReference
	var after guid.ID
	for {
		rows, err := ref.WithContext(ctx).Where(ref.ID.Gt(after), ref.State.Eq("active"), ref.Purpose.In("avatar", "avatar_candidate")).Order(ref.ID.Asc()).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, legacy := range rows {
			err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				q := query.Bind(tx, r.prefix)
				current, err := q.AccountReference.WithContext(ctx).Where(q.AccountReference.ID.Eq(legacy.ID), q.AccountReference.State.Eq("active")).Take()
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				object, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(current.ObjectID), q.Object.State.Eq("verified")).Take()
				if err != nil {
					return err
				}
				purpose, slot, replace := PrincipalAvatarPurpose, "current", true
				var expiry *time.Time
				if current.Purpose == "avatar_candidate" {
					purpose, replace = PrincipalAvatarCandidatePurpose, false
					slot, err = candidateSlotFromKey(object.StorageKey)
					if err != nil {
						return err
					}
					if current.ExpiresAt == nil || *current.ExpiresAt <= now.UnixMilli() {
						_, updateErr := q.AccountReference.WithContext(ctx).Where(q.AccountReference.ID.Eq(current.ID)).Update(q.AccountReference.State, "expired")
						return updateErr
					}
					value := time.UnixMilli(*current.ExpiresAt)
					expiry = &value
				}
				existing, err := q.ResourceBinding.WithContext(ctx).Where(
					q.ResourceBinding.ResourceKind.Eq("principal"), q.ResourceBinding.ResourceID.Eq(current.PrincipalID),
					q.ResourceBinding.Purpose.Eq(purpose), q.ResourceBinding.Slot.Eq(slot), q.ResourceBinding.State.Eq("active"),
				).Count()
				if err != nil {
					return err
				}
				if existing == 0 {
					if _, err := r.createExisting(ctx, tx, AccountResourceInput{
						PrincipalID: current.PrincipalID, Purpose: purpose, Slot: slot,
						Name: path.Base(object.StorageKey), ExpiresAt: expiry, ActorID: current.PrincipalID, Replace: replace,
					}, object.ID, now); err != nil {
						return err
					}
				}
				_, err = q.AccountReference.WithContext(ctx).Where(q.AccountReference.ID.Eq(current.ID), q.AccountReference.State.Eq("active")).Update(q.AccountReference.State, "inactive")
				return err
			})
			if err != nil {
				return fmt.Errorf("migrate legacy account reference %s: %w", legacy.ID, err)
			}
		}
		if len(rows) < 100 {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}
