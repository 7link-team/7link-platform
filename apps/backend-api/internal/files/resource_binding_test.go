package files

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var errResourceOwnerMismatch = errors.New("resource owner mismatch")

func resourceBindingPurpose(scopes ...string) ResourcePurpose {
	return ResourcePurpose{
		Name: "principal.avatar", ResourceKind: "principal", OwnerScopes: scopes, VersionMode: "head",
		ValidateResource: func(_ context.Context, _ *gorm.DB, resourceID guid.ID, owner ResourceOwner) error {
			if owner.Scope != "account" || owner.ID != resourceID {
				return errResourceOwnerMismatch
			}
			return nil
		},
	}
}

type resourceBindingHarness struct {
	db       *gorm.DB
	tree     directoryTree
	bindings *ResourceBindings
	q        *query.Query
	now      time.Time
}

func newResourceBindingHarness(t *testing.T) *resourceBindingHarness {
	t.Helper()
	db, accounts, _ := newAccountCleanupHarness(t)
	h := &resourceBindingHarness{
		db: db, tree: directoryTree{prefix: accounts.prefix}, now: time.Now().UTC(),
	}
	require.NoError(t, h.tree.createSpace(t.Context(), db, 1, 2, 70, "account", h.now))
	bindRootFixture(t, db, h.tree, 2, rootBinding{scope: "account", ownerID: 70})
	replaceACLFixture(t, db, h.tree, 2, true, nodeACLRule{
		subject: aclSubject{kind: "owner", id: 70}, effect: "allow",
		actions: []string{"list", "traverse", "read", "download", "create", "update", "rename", "move", "delete", "restore", "purge", "share", "manage_acl"},
		applies: "self_and_descendants",
	})
	var err error
	h.bindings, err = NewResourceBindings(accounts.prefix)
	require.NoError(t, err)
	h.q = query.Bind(db, accounts.prefix)
	return h
}

func (h *resourceBindingHarness) file(t *testing.T, id, versionID, objectID guid.ID, name string) *model.Node {
	t.Helper()
	var row *model.Node
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = h.tree.createNodeOn(t.Context(), tx, 1, id, 2, "file", name, h.now)
		return err
	}))
	require.NoError(t, h.q.Object.WithContext(t.Context()).Create(&model.Object{
		ID: objectID, BackendID: "local", StorageKey: "objects/" + objectID.String(), URI: "file:///objects/" + objectID.String(),
		State: "verified", Size: 3, MIME: "image/png", Type: "image", Subtype: "png",
		MetadataJSON: "{}", MetadataState: "unknown", Version: 1, CreatedAt: h.now.UnixMilli(),
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.tree.appendVersion(t.Context(), tx, model.Version{
			ID: versionID, SpaceID: 1, NodeID: id, Number: 1, ObjectID: objectID,
			Ext: "png", DeclaredMIME: "image/png", CreatedBy: 70,
		}, row.Version, h.now)
	}))
	row, err := h.q.Node.WithContext(t.Context()).Where(h.q.Node.ID.Eq(id)).Take()
	require.NoError(t, err)
	return row
}

func TestResourcePurposeRegistryRejectsAmbiguousContracts(t *testing.T) {
	bindings, err := NewResourceBindings("")
	require.NoError(t, err)
	require.Error(t, bindings.RegisterPurpose(ResourcePurpose{}))
	require.Error(t, bindings.RegisterPurpose(ResourcePurpose{
		Name: "principal.avatar", ResourceKind: "principal", OwnerScopes: []string{"account"}, VersionMode: "head",
	}))
	duplicateScopes := resourceBindingPurpose("account", "account")
	require.Error(t, bindings.RegisterPurpose(duplicateScopes))

	purpose := resourceBindingPurpose("tenant", "account")
	require.NoError(t, bindings.RegisterPurpose(purpose))
	purpose.OwnerScopes[0] = "system"
	registered, ok := bindings.purpose("principal.avatar")
	require.True(t, ok)
	require.Equal(t, []string{"account", "tenant"}, registered.OwnerScopes, "the registry must own an immutable scope copy")
	require.Error(t, bindings.RegisterPurpose(resourceBindingPurpose("account")), "purpose names are unique")
	_, err = NewResourceBindings("invalid-prefix;")
	require.Error(t, err)
}

func TestResourceBindingLifecycleIsAtomicScopedAndHidden(t *testing.T) {
	h := newResourceBindingHarness(t)
	require.NoError(t, h.bindings.RegisterPurpose(resourceBindingPurpose("account")))
	file := h.file(t, 3, 30, 300, "avatar.png")
	userFile := h.file(t, 4, 40, 400, "notes.png")
	beforeSpace := treeSpaceRevision(t, h.db, h.tree.prefix, 1)

	var binding *model.ResourceBinding
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		binding, err = h.bindings.Bind(t.Context(), tx, ResourceBindingInput{
			ID: 100, SpaceID: 1, FileID: file.ID, ExpectedFileVersion: file.Version,
			Purpose: "principal.avatar", ResourceKind: "principal", ResourceID: 70,
			Slot: "current", ActorID: 70,
		}, h.now)
		return err
	}))
	require.Equal(t, guid.ID(100), binding.ID)
	require.Nil(t, binding.VersionID, "head bindings follow the file's current immutable version")
	require.Equal(t, beforeSpace+1, treeSpaceRevision(t, h.db, h.tree.prefix, 1))
	managed := treeNode(t, h.db, h.tree.prefix, file.ID)
	require.Equal(t, "resource", managed.ManagementMode)
	require.Equal(t, file.Version+1, managed.Version)

	authorizer, err := newAccountNodeAuthorizer(h.tree, 70)
	require.NoError(t, err)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return authorizer.access(t.Context(), tx, 1, 70, file.ID, "read")
	}), errOperationForbidden, "ordinary file authorization must not expose resource-managed content")
	var page fileChildrenPage
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		page, err = h.tree.authorizedChildren(t.Context(), tx, authorizer, 1, 2, browserPageRequest{Limit: 100})
		return err
	}))
	require.Len(t, page.Items, 1)
	require.Equal(t, userFile.ID, page.Items[0].ID)

	orphans, err := h.bindings.OrphanedFiles(t.Context(), h.db, 0, 100)
	require.NoError(t, err)
	require.Empty(t, orphans)
	beforeUnbind := treeSpaceRevision(t, h.db, h.tree.prefix, 1)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.bindings.Unbind(t.Context(), tx, 1, binding.ID, binding.Version, h.now.Add(time.Second))
	}))
	require.Equal(t, beforeUnbind+1, treeSpaceRevision(t, h.db, h.tree.prefix, 1))
	orphans, err = h.bindings.OrphanedFiles(t.Context(), h.db, 0, 100)
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	require.Equal(t, file.ID, orphans[0].ID)
	require.NotEqual(t, userFile.ID, orphans[0].ID, "unbound user-managed files are valid, not business orphans")

	staleRevision := treeSpaceRevision(t, h.db, h.tree.prefix, 1)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.bindings.Unbind(t.Context(), tx, 1, binding.ID, binding.Version, h.now.Add(2*time.Second))
	}), errResourceBindingConflict)
	require.Equal(t, staleRevision, treeSpaceRevision(t, h.db, h.tree.prefix, 1))
}

func TestResourceBindingRejectsWrongOwnerVersionScopeAndDuplicateSlot(t *testing.T) {
	h := newResourceBindingHarness(t)
	require.NoError(t, h.bindings.RegisterPurpose(resourceBindingPurpose("account")))
	first := h.file(t, 3, 30, 300, "first.png")
	second := h.file(t, 4, 40, 400, "second.png")
	base := ResourceBindingInput{
		ID: 100, SpaceID: 1, FileID: first.ID, ExpectedFileVersion: first.Version,
		Purpose: "principal.avatar", ResourceKind: "principal", ResourceID: 71,
		Slot: "current", ActorID: 70,
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.bindings.Bind(t.Context(), tx, base, h.now)
		return err
	}), errResourceOwnerMismatch)
	require.Equal(t, "user", treeNode(t, h.db, h.tree.prefix, first.ID).ManagementMode)

	foreignVersion := guid.ID(40)
	pinned, err := NewResourceBindings(h.tree.prefix)
	require.NoError(t, err)
	pinnedPurpose := resourceBindingPurpose("account")
	pinnedPurpose.Name, pinnedPurpose.VersionMode = "principal.avatar-history", "pinned"
	require.NoError(t, pinned.RegisterPurpose(pinnedPurpose))
	base.Purpose, base.ResourceID, base.VersionID = pinnedPurpose.Name, 70, &foreignVersion
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := pinned.Bind(t.Context(), tx, base, h.now)
		return err
	}), errResourceBinding)

	base.Purpose, base.VersionID = "principal.avatar", nil
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.bindings.Bind(t.Context(), tx, base, h.now)
		return err
	}))
	base.ID, base.FileID, base.ExpectedFileVersion = 101, second.ID, second.Version
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.bindings.Bind(t.Context(), tx, base, h.now)
		return err
	}), errResourceBindingConflict)
	require.Equal(t, "user", treeNode(t, h.db, h.tree.prefix, second.ID).ManagementMode, "failed binding must not partially change the logical file")

	wrongScope, err := NewResourceBindings(h.tree.prefix)
	require.NoError(t, err)
	require.NoError(t, wrongScope.RegisterPurpose(resourceBindingPurpose("entity")))
	base.ID, base.Slot = 102, "alternate"
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := wrongScope.Bind(t.Context(), tx, base, h.now)
		return err
	}), errResourcePurpose)
}

func TestResourceBindingRollsBackLedgerWhenNodeTransitionFails(t *testing.T) {
	h := newResourceBindingHarness(t)
	require.NoError(t, h.bindings.RegisterPurpose(resourceBindingPurpose("account")))
	file := h.file(t, 3, 30, 300, "avatar.png")
	trigger := query.BindNames(h.tree.prefix).Nodes.String() + "_reject_resource"
	require.NoError(t, h.db.Exec("CREATE TRIGGER "+trigger+" BEFORE UPDATE OF management_mode ON "+query.BindNames(h.tree.prefix).Nodes.String()+" WHEN NEW.id = 3 BEGIN SELECT RAISE(ABORT, 'injected node failure'); END").Error)
	t.Cleanup(func() { _ = h.db.Exec("DROP TRIGGER IF EXISTS " + trigger).Error })

	err := h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.bindings.Bind(t.Context(), tx, ResourceBindingInput{
			ID: 100, SpaceID: 1, FileID: file.ID, ExpectedFileVersion: file.Version,
			Purpose: "principal.avatar", ResourceKind: "principal", ResourceID: 70,
			Slot: "current", ActorID: 70,
		}, h.now)
		return err
	})
	require.ErrorContains(t, err, "injected node failure")
	count, err := h.q.ResourceBinding.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, "user", treeNode(t, h.db, h.tree.prefix, file.ID).ManagementMode)
}

func TestResourceBindingExpiryIsBoundedAndCreatesAnOrphan(t *testing.T) {
	h := newResourceBindingHarness(t)
	purpose := resourceBindingPurpose("account")
	purpose.Name, purpose.AllowExpiry = "principal.avatar-candidate", true
	require.NoError(t, h.bindings.RegisterPurpose(purpose))
	file := h.file(t, 3, 30, 300, "candidate.png")
	expiresAt := h.now.Add(time.Hour)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.bindings.Bind(t.Context(), tx, ResourceBindingInput{
			ID: 100, SpaceID: 1, FileID: file.ID, ExpectedFileVersion: file.Version,
			Purpose: purpose.Name, ResourceKind: "principal", ResourceID: 70,
			Slot: "candidate-1", ExpiresAt: &expiresAt, ActorID: 70,
		}, h.now)
		return err
	}))

	after, err := h.bindings.Expire(t.Context(), h.db, h.now, 0, 1)
	require.NoError(t, err)
	require.Zero(t, after)
	active, err := h.q.ResourceBinding.WithContext(t.Context()).Where(h.q.ResourceBinding.ID.Eq(guid.ID(100))).Take()
	require.NoError(t, err)
	require.Equal(t, "active", active.State)

	before := treeSpaceRevision(t, h.db, h.tree.prefix, 1)
	after, err = h.bindings.Expire(t.Context(), h.db, expiresAt.Add(time.Second), 0, 1)
	require.NoError(t, err)
	require.Equal(t, guid.ID(100), after)
	require.Equal(t, before+1, treeSpaceRevision(t, h.db, h.tree.prefix, 1))
	expired, err := h.q.ResourceBinding.WithContext(t.Context()).Where(h.q.ResourceBinding.ID.Eq(guid.ID(100))).Take()
	require.NoError(t, err)
	require.Equal(t, "expired", expired.State)
	require.Nil(t, expired.LiveSlot)
	require.Equal(t, int64(2), expired.Version)
	after, err = h.bindings.Expire(t.Context(), h.db, expiresAt.Add(time.Second), after, 1)
	require.NoError(t, err)
	require.Zero(t, after)
	orphans, err := h.bindings.OrphanedFiles(t.Context(), h.db, 0, 100)
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	require.Equal(t, file.ID, orphans[0].ID)
}

func TestResourceBindingDatabaseConstraintsAndPopulatedRollback(t *testing.T) {
	h := newResourceBindingHarness(t)
	file := h.file(t, 3, 30, 300, "avatar.png")
	second := h.file(t, 4, 40, 400, "other.png")
	live := 1
	invalid := &model.ResourceBinding{
		ID: 100, SpaceID: 1, FileID: 2, FileKind: "file", Purpose: "principal.avatar",
		ResourceKind: "principal", ResourceID: 70, Slot: "current", State: "active", LiveSlot: &live,
		Version: 1, CreatedBy: 70, CreatedAt: h.now.UnixMilli(), UpdatedAt: h.now.UnixMilli(),
	}
	require.Error(t, h.q.ResourceBinding.WithContext(t.Context()).Create(invalid), "directories cannot be business file bindings")
	invalid.ID, invalid.FileID = 101, file.ID
	badVersion := guid.ID(999)
	invalid.VersionID = &badVersion
	require.Error(t, h.q.ResourceBinding.WithContext(t.Context()).Create(invalid), "pinned versions must belong to the logical file")

	invalid.ID, invalid.VersionID = 102, nil
	require.NoError(t, h.q.ResourceBinding.WithContext(t.Context()).Create(invalid))
	duplicate := *invalid
	duplicate.ID, duplicate.FileID = 103, second.ID
	require.Error(t, h.q.ResourceBinding.WithContext(t.Context()).Create(&duplicate), "only one active binding may occupy a resource slot")

	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = h.tree.prefix
	require.NoError(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, "sqlite", 9))
	require.False(t, h.db.Migrator().HasTable(query.BindNames(h.tree.prefix).ResourceBindings.String()))
	require.False(t, h.db.Migrator().HasColumn(query.BindNames(h.tree.prefix).Nodes.String(), "management_mode"))
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
	restored, err := query.Bind(h.db, h.tree.prefix).Node.WithContext(t.Context()).Where(query.Bind(h.db, h.tree.prefix).Node.ID.Eq(file.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "user", restored.ManagementMode)
}

func TestSystemSpaceHasNoSyntheticTenantOrPrincipalOwner(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	now := time.Now().UTC()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.createSystemSpaceOn(t.Context(), tx, 1, 2, now)
	}))
	q := query.Bind(db, tree.prefix)
	space, err := q.Space.WithContext(t.Context()).Where(q.Space.ID.Eq(guid.ID(1))).Take()
	require.NoError(t, err)
	require.Equal(t, "system", space.ScopeKind)
	require.Nil(t, space.TenantID)
	require.Nil(t, space.PrincipalID)
	require.NotNil(t, space.SystemSlot)
	require.Equal(t, 1, *space.SystemSlot)
	root := treeNode(t, db, tree.prefix, 2)
	require.Nil(t, root.RootScope)
	require.Nil(t, root.RootOwnerID)
	require.True(t, root.ACLStopInheritance)

	owner, err := tree.resourceOwner(t.Context(), db, 1, root.ID)
	require.NoError(t, err)
	require.Equal(t, ResourceOwner{Scope: "system"}, owner)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.createSystemSpaceOn(t.Context(), tx, 3, 4, now)
	}), "there is exactly one platform-owned system space")

	slot := 1
	fakeTenant := guid.ID(99)
	require.Error(t, q.Space.WithContext(t.Context()).Create(&model.Space{
		ID: 5, ScopeKind: "system", TenantID: &fakeTenant, SystemSlot: &slot, Version: 1, CreatedAt: now.UnixMilli(),
	}), "system ownership cannot be represented by a sentinel tenant")
	require.Error(t, q.Space.WithContext(t.Context()).Create(&model.Space{
		ID: 6, ScopeKind: "tenant", TenantID: &fakeTenant, SystemSlot: &slot, Version: 1, CreatedAt: now.UnixMilli(),
	}), "organization spaces cannot occupy the system singleton slot")
	var file *model.Node
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		file, err = tree.createNodeOn(t.Context(), tx, 1, 7, root.ID, "file", "template.md", now)
		return err
	}))
	require.NoError(t, q.Object.WithContext(t.Context()).Create(&model.Object{
		ID: 8, BackendID: "local", StorageKey: "objects/8", URI: "file:///objects/8", State: "verified",
		Size: 3, MIME: "text/markdown", Type: "text", Subtype: "markdown", MetadataJSON: "{}",
		MetadataState: "unknown", Version: 1, CreatedAt: now.UnixMilli(),
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.appendVersion(t.Context(), tx, model.Version{
			ID: 9, SpaceID: 1, NodeID: file.ID, Number: 1, ObjectID: 8,
			Ext: "md", DeclaredMIME: "text/markdown", CreatedBy: 70,
		}, file.Version, now)
	}))
	file = treeNode(t, db, tree.prefix, file.ID)
	bindings, err := NewResourceBindings(tree.prefix)
	require.NoError(t, err)
	require.NoError(t, bindings.RegisterPurpose(ResourcePurpose{
		Name: "platform.template", ResourceKind: "template", OwnerScopes: []string{"system"}, VersionMode: "head",
		ValidateResource: func(_ context.Context, _ *gorm.DB, resourceID guid.ID, owner ResourceOwner) error {
			require.Equal(t, guid.ID(500), resourceID)
			require.Equal(t, ResourceOwner{Scope: "system"}, owner)
			return nil
		},
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bindings.Bind(t.Context(), tx, ResourceBindingInput{
			ID: 10, SpaceID: 1, FileID: file.ID, ExpectedFileVersion: file.Version,
			Purpose: "platform.template", ResourceKind: "template", ResourceID: 500,
			Slot: "default", ActorID: 70,
		}, now)
		return err
	}))
	require.Equal(t, "resource", treeNode(t, db, tree.prefix, file.ID).ManagementMode)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = tree.prefix
	require.Error(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, "sqlite", 9), "downgrade must refuse to erase an initialized system scope")
	space, err = q.Space.WithContext(t.Context()).Where(q.Space.ID.Eq(guid.ID(1))).Take()
	require.NoError(t, err)
	require.Equal(t, "system", space.ScopeKind)
}

func TestResourceBindingRejectsInvalidCallsAndFilesWithoutVersions(t *testing.T) {
	h := newResourceBindingHarness(t)
	require.NoError(t, h.bindings.RegisterPurpose(resourceBindingPurpose("account")))
	file := h.file(t, 3, 30, 300, "avatar.png")
	base := ResourceBindingInput{
		ID: 100, SpaceID: 1, FileID: file.ID, ExpectedFileVersion: file.Version,
		Purpose: "principal.avatar", ResourceKind: "principal", ResourceID: 70,
		Slot: "current", ActorID: 70,
	}

	_, err := h.bindings.Bind(t.Context(), h.db, base, h.now)
	require.ErrorIs(t, err, ErrAccountTransaction)
	for name, mutate := range map[string]func(ResourceBindingInput) ResourceBindingInput{
		"missing id":           func(in ResourceBindingInput) ResourceBindingInput { in.ID = 0; return in },
		"missing space":        func(in ResourceBindingInput) ResourceBindingInput { in.SpaceID = 0; return in },
		"missing file":         func(in ResourceBindingInput) ResourceBindingInput { in.FileID = 0; return in },
		"missing file version": func(in ResourceBindingInput) ResourceBindingInput { in.ExpectedFileVersion = 0; return in },
		"missing resource":     func(in ResourceBindingInput) ResourceBindingInput { in.ResourceID = 0; return in },
		"missing actor":        func(in ResourceBindingInput) ResourceBindingInput { in.ActorID = 0; return in },
		"invalid slot":         func(in ResourceBindingInput) ResourceBindingInput { in.Slot = "not a slot"; return in },
	} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, bindErr := h.bindings.Bind(t.Context(), tx, mutate(base), h.now)
			require.ErrorIs(t, bindErr, errResourceBinding, name)
			return nil
		}))
	}

	for name, mutate := range map[string]func(ResourceBindingInput) ResourceBindingInput{
		"unknown purpose": func(in ResourceBindingInput) ResourceBindingInput { in.Purpose = "principal.unknown"; return in },
		"wrong kind":      func(in ResourceBindingInput) ResourceBindingInput { in.ResourceKind = "entity"; return in },
		"head is pinned": func(in ResourceBindingInput) ResourceBindingInput {
			versionID := guid.ID(30)
			in.VersionID = &versionID
			return in
		},
		"unexpected expiry": func(in ResourceBindingInput) ResourceBindingInput {
			expires := h.now.Add(time.Hour)
			in.ExpiresAt = &expires
			return in
		},
	} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, bindErr := h.bindings.Bind(t.Context(), tx, mutate(base), h.now)
			require.ErrorIs(t, bindErr, errResourcePurpose, name)
			return nil
		}))
	}

	var nilBindings *ResourceBindings
	_, ok := nilBindings.purpose("principal.avatar")
	require.False(t, ok)
	require.False(t, validPurpose(ResourcePurpose{
		Name: "principal.avatar", ResourceKind: "principal", OwnerScopes: []string{"unknown"}, VersionMode: "head",
		ValidateResource: resourceBindingPurpose("account").ValidateResource,
	}))

	var unversioned *model.Node
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var createErr error
		unversioned, createErr = h.tree.createNodeOn(t.Context(), tx, 1, 4, 2, "file", "empty.png", h.now)
		return createErr
	}))
	base.ID, base.FileID, base.ExpectedFileVersion, base.Slot = 101, unversioned.ID, unversioned.Version, "empty"
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, bindErr := h.bindings.Bind(t.Context(), tx, base, h.now)
		return bindErr
	}), errResourceBinding)
	require.Equal(t, "user", treeNode(t, h.db, h.tree.prefix, unversioned.ID).ManagementMode)
}

func TestResourceBindingDatabaseFailuresRollBackMutations(t *testing.T) {
	for _, table := range []string{"nodes", "versions", "resource bindings"} {
		t.Run(table, func(t *testing.T) {
			h := newResourceBindingHarness(t)
			require.NoError(t, h.bindings.RegisterPurpose(resourceBindingPurpose("account")))
			file := h.file(t, 3, 30, 300, "avatar.png")
			names := query.BindNames(h.tree.prefix)
			tableName := map[string]string{
				"nodes":             names.Nodes.String(),
				"versions":          names.Versions.String(),
				"resource bindings": names.ResourceBindings.String(),
			}[table]
			fault := failTableAfter(t, h.db, tableName, 0)
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, bindErr := h.bindings.Bind(t.Context(), tx, ResourceBindingInput{
					ID: 100, SpaceID: 1, FileID: file.ID, ExpectedFileVersion: file.Version,
					Purpose: "principal.avatar", ResourceKind: "principal", ResourceID: 70,
					Slot: "current", ActorID: 70,
				}, h.now)
				return bindErr
			})
			require.True(t, fault.Fired())
			require.ErrorIs(t, err, errInjectedTableFault)
			if tableName != names.Nodes.String() {
				require.Equal(t, "user", treeNode(t, h.db, h.tree.prefix, file.ID).ManagementMode)
			}
			if tableName != names.ResourceBindings.String() {
				count, countErr := h.q.ResourceBinding.WithContext(t.Context()).Count()
				require.NoError(t, countErr)
				require.Zero(t, count)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		table func(query.TableNames) string
		skip  int64
	}{
		{name: "node transition", table: func(names query.TableNames) string { return names.Nodes.String() }, skip: 2},
		{name: "space revision", table: func(names query.TableNames) string { return names.Spaces.String() }, skip: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newResourceBindingHarness(t)
			require.NoError(t, h.bindings.RegisterPurpose(resourceBindingPurpose("account")))
			file := h.file(t, 3, 30, 300, "avatar.png")
			fault := failTableAfter(t, h.db, tc.table(query.BindNames(h.tree.prefix)), tc.skip)
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, bindErr := h.bindings.Bind(t.Context(), tx, ResourceBindingInput{
					ID: 100, SpaceID: 1, FileID: file.ID, ExpectedFileVersion: file.Version,
					Purpose: "principal.avatar", ResourceKind: "principal", ResourceID: 70,
					Slot: "current", ActorID: 70,
				}, h.now)
				return bindErr
			})
			require.True(t, fault.Fired())
			require.ErrorIs(t, err, errInjectedTableFault)
			count, countErr := h.q.ResourceBinding.WithContext(t.Context()).Count()
			require.NoError(t, countErr)
			require.Zero(t, count)
			if tc.table(query.BindNames(h.tree.prefix)) != query.BindNames(h.tree.prefix).Nodes.String() {
				require.Equal(t, "user", treeNode(t, h.db, h.tree.prefix, file.ID).ManagementMode)
			}
		})
	}
}

func TestResourceBindingTransitionFailuresPreserveTheActiveBinding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table func(query.TableNames) string
		skip  int64
	}{
		{name: "space lock", table: func(names query.TableNames) string { return names.Spaces.String() }},
		{name: "binding update", table: func(names query.TableNames) string { return names.ResourceBindings.String() }},
		{name: "space revision", table: func(names query.TableNames) string { return names.Spaces.String() }, skip: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newResourceBindingHarness(t)
			require.NoError(t, h.bindings.RegisterPurpose(resourceBindingPurpose("account")))
			file := h.file(t, 3, 30, 300, "avatar.png")
			var bound *model.ResourceBinding
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var bindErr error
				bound, bindErr = h.bindings.Bind(t.Context(), tx, ResourceBindingInput{
					ID: 100, SpaceID: 1, FileID: file.ID, ExpectedFileVersion: file.Version,
					Purpose: "principal.avatar", ResourceKind: "principal", ResourceID: 70,
					Slot: "current", ActorID: 70,
				}, h.now)
				return bindErr
			}))
			names := query.BindNames(h.tree.prefix)
			fault := failTableAfter(t, h.db, tc.table(names), tc.skip)
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return h.bindings.Unbind(t.Context(), tx, 1, bound.ID, bound.Version, h.now.Add(time.Second))
			})
			require.True(t, fault.Fired())
			require.ErrorIs(t, err, errInjectedTableFault)
			if tc.table(names) != names.ResourceBindings.String() {
				row, rowErr := h.q.ResourceBinding.WithContext(t.Context()).Where(h.q.ResourceBinding.ID.Eq(bound.ID)).Take()
				require.NoError(t, rowErr)
				require.Equal(t, "active", row.State)
				require.Equal(t, bound.Version, row.Version)
			}
		})
	}

	h := newResourceBindingHarness(t)
	var nilBindings *ResourceBindings
	require.ErrorIs(t, nilBindings.Unbind(t.Context(), h.db, 1, 1, 1, h.now), errResourceBinding)
	require.ErrorIs(t, h.bindings.transition(t.Context(), h.db, 1, 1, 1, "invalid", h.now), errResourceBinding)
	require.ErrorIs(t, h.bindings.Unbind(t.Context(), h.db, 1, 1, 1, h.now), ErrAccountTransaction)
}

func TestResourceBindingPagingPropagatesQueryFailures(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		h := newResourceBindingHarness(t)
		fault := failTableAfter(t, h.db, query.BindNames(h.tree.prefix).ResourceBindings.String(), 0)
		next, err := h.bindings.Expire(t.Context(), h.db, h.now, 37, 10)
		require.True(t, fault.Fired())
		require.ErrorIs(t, err, errInjectedTableFault)
		require.Equal(t, guid.ID(37), next)
	})

	t.Run("orphans", func(t *testing.T) {
		h := newResourceBindingHarness(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		rows, err := h.bindings.OrphanedFiles(ctx, h.db, 37, 10)
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, rows)
	})

	h := newResourceBindingHarness(t)
	for name, call := range map[string]func() error{
		"invalid expiry": func() error {
			_, err := h.bindings.Expire(t.Context(), h.db, h.now, -1, 10)
			return err
		},
		"invalid orphan page": func() error {
			_, err := h.bindings.OrphanedFiles(t.Context(), h.db, 0, 0)
			return err
		},
	} {
		require.ErrorIs(t, call(), errResourceBinding, name)
	}
}
