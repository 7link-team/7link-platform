package files

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type accountResourceIDs struct{ next atomic.Int64 }

func (ids *accountResourceIDs) Next() (int64, error) { return ids.next.Add(1), nil }

// failingIDs stops handing out identifiers after a fixed number of calls so the
// allocation failures inside create/ensureRoot are reachable.
type failingIDs struct {
	remaining atomic.Int64
	next      atomic.Int64
}

func (ids *failingIDs) Next() (int64, error) {
	if ids.remaining.Add(-1) < 0 {
		return 0, ErrUnsupported
	}
	return ids.next.Add(1), nil
}

type accountResourceHarness struct {
	db       *gorm.DB
	driver   *Local
	accounts *AccountObjects
	resource *AccountResourceFiles
	q        *query.Query
	now      time.Time
	events   []audit.DomainEvent
}

func accountAvatarPurpose(name string, expiry bool) ResourcePurpose {
	return ResourcePurpose{
		Name: name, ResourceKind: "principal", OwnerScopes: []string{"account"},
		VersionMode: "head", AllowExpiry: expiry,
		ValidateResource: func(_ context.Context, _ *gorm.DB, resourceID guid.ID, owner ResourceOwner) error {
			if owner.Scope != "account" || owner.ID != resourceID {
				return errResourceOwnerMismatch
			}
			return nil
		},
	}
}

func newAccountResourceHarness(t *testing.T) *accountResourceHarness {
	t.Helper()
	return newAccountResourceHarnessWithIDs(t, &accountResourceIDs{})
}

func newAccountResourceHarnessWithIDs(t *testing.T, ids guid.Source) *accountResourceHarness {
	t.Helper()
	db, accounts, driver := newAccountCleanupHarness(t)
	h := &accountResourceHarness{
		db: db, driver: driver, accounts: accounts,
		q: query.Bind(db, accounts.prefix), now: time.Now().UTC().Truncate(time.Millisecond),
	}
	bindings, err := NewResourceBindings(accounts.prefix)
	require.NoError(t, err)
	h.resource, err = NewAccountResourceFiles(accounts, bindings, driver, accounts.prefix)
	require.NoError(t, err)
	require.NoError(t, h.resource.Configure(ids, func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error {
		h.events = append(h.events, event)
		return nil
	}))
	require.NoError(t, h.resource.RegisterPurpose(accountAvatarPurpose(PrincipalAvatarPurpose, false)))
	require.NoError(t, h.resource.RegisterPurpose(accountAvatarPurpose(PrincipalAvatarCandidatePurpose, true)))
	return h
}

// bind stages bytes and attaches them to one principal slot in a single
// transaction, which is the only boundary the resource files accept.
func (h *accountResourceHarness) bind(t *testing.T, principal guid.ID, purpose, slot, name, content string, expires *time.Time, replace bool) *model.ResourceBinding {
	t.Helper()
	prepared, err := h.resource.StageOn(t.Context(), h.db, principal, "resources/"+name, bytes.NewBufferString(content), int64(len(content)), h.now.Add(time.Hour))
	require.NoError(t, err)
	var binding *model.ResourceBinding
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		binding, err = h.resource.Create(t.Context(), tx, AccountResourceInput{
			Prepared: prepared, PrincipalID: principal, Purpose: purpose, Slot: slot,
			Name: name, ExpiresAt: expires, ActorID: principal, Replace: replace,
		}, h.now)
		return err
	}))
	require.NotNil(t, binding)
	return binding
}

func (h *accountResourceHarness) eventTypes() []string {
	types := make([]string, 0, len(h.events))
	for _, event := range h.events {
		types = append(types, event.EventType)
	}
	return types
}

func TestAccountResourceFilesRejectsIncompleteConstruction(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	bindings, err := NewResourceBindings(accounts.prefix)
	require.NoError(t, err)

	for name, build := range map[string]func() (*AccountResourceFiles, error){
		"no accounts": func() (*AccountResourceFiles, error) {
			return NewAccountResourceFiles(nil, bindings, driver, accounts.prefix)
		},
		"no bindings": func() (*AccountResourceFiles, error) {
			return NewAccountResourceFiles(accounts, nil, driver, accounts.prefix)
		},
		"no driver": func() (*AccountResourceFiles, error) {
			return NewAccountResourceFiles(accounts, bindings, nil, accounts.prefix)
		},
	} {
		built, err := build()
		assert.ErrorIs(t, err, ErrUnsupported, name)
		assert.Nil(t, built, name)
	}

	resource, err := NewAccountResourceFiles(accounts, bindings, driver, accounts.prefix)
	require.NoError(t, err)
	// Configure is what supplies the identifier source and the audit sink, so
	// an unconfigured instance must refuse work rather than write unaudited
	// rows or allocate a zero identifier.
	assert.ErrorIs(t, resource.Configure(nil, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }), ErrUnsupported)
	assert.ErrorIs(t, resource.Configure(&accountResourceIDs{}, nil), ErrUnsupported)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := resource.Create(t.Context(), tx, AccountResourceInput{
			Prepared: &PreparedAccountObject{}, PrincipalID: 70, Purpose: PrincipalAvatarPurpose,
			Slot: "current", Name: "a.png", ActorID: 70,
		}, time.Now())
		assert.ErrorIs(t, err, ErrUnsupported)
		return nil
	}))

	var nilResource *AccountResourceFiles
	assert.ErrorIs(t, nilResource.Configure(&accountResourceIDs{}, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }), ErrUnsupported)
	assert.ErrorIs(t, nilResource.RegisterPurpose(ResourcePurpose{}), ErrUnsupported)
	_, err = nilResource.Expire(t.Context(), db, time.Now(), 0, 10)
	assert.ErrorIs(t, err, ErrUnsupported)
	assert.ErrorIs(t, nilResource.MigrateLegacyAvatars(t.Context(), db, time.Now()), ErrUnsupported)
}

func TestAccountResourceLifecycleBindsReadsAndReleases(t *testing.T) {
	h := newAccountResourceHarness(t)
	binding := h.bind(t, 70, PrincipalAvatarPurpose, "current", "avatar.png", "abc", nil, false)
	assert.Equal(t, "active", binding.State)
	assert.EqualValues(t, 70, binding.ResourceID)

	// The first bind has to build the account space, its root and the owner
	// ACL, and both the root creation and the binding must be audited.
	assert.Contains(t, h.eventTypes(), "file_root_created")
	assert.Contains(t, h.eventTypes(), "file_resource_bound")

	present, err := h.resource.Has(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	assert.True(t, present)

	read, err := h.resource.Open(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	defer func() { require.NoError(t, read.Reader.Close()) }()
	assert.Equal(t, "avatar.png", read.Name)
	assert.EqualValues(t, 3, read.Size)
	body, err := io.ReadAll(read.Reader)
	require.NoError(t, err)
	assert.Equal(t, "abc", string(body))

	// Releasing the principal drops every active binding it owns.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.resource.Release(t.Context(), tx, 70, h.now)
	}))
	present, err = h.resource.Has(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	assert.False(t, present)
	_, err = h.resource.Open(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAccountResourceReplaceRetiresThePreviousSlotOnly(t *testing.T) {
	h := newAccountResourceHarness(t)
	first := h.bind(t, 70, PrincipalAvatarPurpose, "current", "first.png", "one", nil, false)
	other := h.bind(t, 70, PrincipalAvatarPurpose, "backup", "other.png", "two", nil, false)
	second := h.bind(t, 70, PrincipalAvatarPurpose, "current", "second.png", "three", nil, true)

	state := func(id guid.ID) string {
		row, err := h.q.ResourceBinding.WithContext(t.Context()).Where(h.q.ResourceBinding.ID.Eq(id)).Take()
		require.NoError(t, err)
		return row.State
	}
	// Replace is slot-scoped: the same slot is retired, a different slot of the
	// same purpose keeps serving.
	assert.NotEqual(t, "active", state(first.ID))
	assert.Equal(t, "active", state(second.ID))
	assert.Equal(t, "active", state(other.ID))

	read, err := h.resource.Open(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	defer func() { require.NoError(t, read.Reader.Close()) }()
	assert.Equal(t, "second.png", read.Name)
}

func TestAccountResourceIsolatesPrincipalsAndUnknownSlots(t *testing.T) {
	h := newAccountResourceHarness(t)
	h.bind(t, 70, PrincipalAvatarPurpose, "current", "avatar.png", "abc", nil, false)

	// A different principal, purpose or slot must never resolve to another
	// principal's bytes.
	for name, lookup := range map[string][3]string{
		"other purpose": {PrincipalAvatarCandidatePurpose, "current", "70"},
		"other slot":    {PrincipalAvatarPurpose, "missing", "70"},
	} {
		_, err := h.resource.Open(t.Context(), h.db, 70, lookup[0], lookup[1], h.now)
		assert.ErrorIs(t, err, ErrNotFound, name)
	}
	_, err := h.resource.Open(t.Context(), h.db, 71, PrincipalAvatarPurpose, "current", h.now)
	assert.ErrorIs(t, err, ErrNotFound)
	present, err := h.resource.Has(t.Context(), h.db, 71, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	assert.False(t, present)
}

func TestAccountResourcePromoteMovesCandidateIntoTheTargetSlot(t *testing.T) {
	h := newAccountResourceHarness(t)
	current := h.bind(t, 70, PrincipalAvatarPurpose, "current", "old.png", "old", nil, false)
	expiry := h.now.Add(time.Hour)
	candidate := h.bind(t, 70, PrincipalAvatarCandidatePurpose, "candidate-a", "new.png", "new", &expiry, false)

	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.resource.Promote(t.Context(), tx, 70, "candidate-a", PrincipalAvatarPurpose, "current", 70, h.now)
	}))

	// The promoted file now serves the target slot, the candidate binding is
	// consumed, and the previous current binding is retired.
	read, err := h.resource.Open(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	defer func() { require.NoError(t, read.Reader.Close()) }()
	assert.Equal(t, "new.png", read.Name)
	assert.Equal(t, candidate.FileID, read.FileID)

	previous, err := h.q.ResourceBinding.WithContext(t.Context()).Where(h.q.ResourceBinding.ID.Eq(current.ID)).Take()
	require.NoError(t, err)
	assert.NotEqual(t, "active", previous.State)
	consumed, err := h.q.ResourceBinding.WithContext(t.Context()).Where(h.q.ResourceBinding.ID.Eq(candidate.ID)).Take()
	require.NoError(t, err)
	assert.NotEqual(t, "active", consumed.State)

	present, err := h.resource.Has(t.Context(), h.db, 70, PrincipalAvatarCandidatePurpose, "candidate-a", h.now)
	require.NoError(t, err)
	assert.False(t, present)
}

func TestAccountResourcePromoteRefusesAbsentAndExpiredCandidates(t *testing.T) {
	h := newAccountResourceHarness(t)
	expiry := h.now.Add(time.Hour)
	h.bind(t, 70, PrincipalAvatarCandidatePurpose, "candidate-a", "new.png", "new", &expiry, false)

	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		assert.ErrorIs(t, h.resource.Promote(t.Context(), tx, 70, "candidate-missing", PrincipalAvatarPurpose, "current", 70, h.now),
			ErrNotFound)
		// A candidate past its expiry must not be promotable, otherwise an
		// abandoned upload could still replace the live avatar later.
		assert.ErrorIs(t, h.resource.Promote(t.Context(), tx, 70, "candidate-a", PrincipalAvatarPurpose, "current", 70, expiry.Add(time.Minute)),
			ErrNotFound)
		// Another principal cannot promote this principal's candidate.
		assert.ErrorIs(t, h.resource.Promote(t.Context(), tx, 71, "candidate-a", PrincipalAvatarPurpose, "current", 71, h.now),
			ErrNotFound)
		return nil
	}))

	for name, call := range map[string]func(tx *gorm.DB) error{
		"no principal": func(tx *gorm.DB) error {
			return h.resource.Promote(t.Context(), tx, 0, "candidate-a", PrincipalAvatarPurpose, "current", 70, h.now)
		},
		"no actor": func(tx *gorm.DB) error {
			return h.resource.Promote(t.Context(), tx, 70, "candidate-a", PrincipalAvatarPurpose, "current", 0, h.now)
		},
	} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			assert.ErrorIs(t, call(tx), ErrInvalidKey, name)
			return nil
		}))
	}
}

func TestAccountResourceExpiredBindingStopsResolvingAndIsCollected(t *testing.T) {
	h := newAccountResourceHarness(t)
	expiry := h.now.Add(time.Hour)
	h.bind(t, 70, PrincipalAvatarCandidatePurpose, "candidate-a", "new.png", "new", &expiry, false)
	after := expiry.Add(time.Minute)

	// Expiry is evaluated against the caller's clock, before any sweep runs.
	present, err := h.resource.Has(t.Context(), h.db, 70, PrincipalAvatarCandidatePurpose, "candidate-a", after)
	require.NoError(t, err)
	assert.False(t, present)
	_, err = h.resource.Open(t.Context(), h.db, 70, PrincipalAvatarCandidatePurpose, "candidate-a", after)
	assert.ErrorIs(t, err, ErrNotFound)

	// A binding with no expiry is untouched by the same sweep.
	h.bind(t, 70, PrincipalAvatarPurpose, "current", "avatar.png", "abc", nil, false)
	_, err = h.resource.Expire(t.Context(), h.db, after, 0, 100)
	require.NoError(t, err)

	rows, err := h.q.ResourceBinding.WithContext(t.Context()).
		Where(h.q.ResourceBinding.Purpose.Eq(PrincipalAvatarCandidatePurpose)).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.NotEqual(t, "active", rows[0].State)

	present, err = h.resource.Has(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", after)
	require.NoError(t, err)
	assert.True(t, present)
}

func TestAccountResourceCreateRejectsMalformedInput(t *testing.T) {
	h := newAccountResourceHarness(t)
	prepared, err := h.resource.StageOn(t.Context(), h.db, 70, "resources/a.png", bytes.NewBufferString("abc"), 3, h.now.Add(time.Hour))
	require.NoError(t, err)
	valid := AccountResourceInput{
		Prepared: prepared, PrincipalID: 70, Purpose: PrincipalAvatarPurpose,
		Slot: "current", Name: "a.png", ActorID: 70,
	}

	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		// Create insists on staged bytes; createExisting insists on an object
		// that already exists. Neither may accept both or neither.
		missingPrepared := valid
		missingPrepared.Prepared = nil
		_, err := h.resource.Create(t.Context(), tx, missingPrepared, h.now)
		assert.ErrorIs(t, err, ErrInvalidKey)

		_, err = h.resource.createExisting(t.Context(), tx, valid, 5, h.now)
		assert.ErrorIs(t, err, ErrInvalidKey)
		_, err = h.resource.createExisting(t.Context(), tx, missingPrepared, 0, h.now)
		assert.ErrorIs(t, err, ErrInvalidKey)

		for name, mutate := range map[string]func(AccountResourceInput) AccountResourceInput{
			"no principal": func(in AccountResourceInput) AccountResourceInput { in.PrincipalID = 0; return in },
			"no actor":     func(in AccountResourceInput) AccountResourceInput { in.ActorID = 0; return in },
			"no name":      func(in AccountResourceInput) AccountResourceInput { in.Name = ""; return in },
		} {
			_, err := h.resource.Create(t.Context(), tx, mutate(valid), h.now)
			assert.ErrorIs(t, err, ErrInvalidKey, name)
		}
		return nil
	}))
}

func TestAccountResourceCreateRequiresAnOpenTransaction(t *testing.T) {
	h := newAccountResourceHarness(t)
	prepared, err := h.resource.StageOn(t.Context(), h.db, 70, "resources/a.png", bytes.NewBufferString("abc"), 3, h.now.Add(time.Hour))
	require.NoError(t, err)
	// Handing the bare connection instead of a transaction must be refused:
	// the tree write, the object attach and the binding have to commit or roll
	// back together.
	_, err = h.resource.Create(t.Context(), h.db, AccountResourceInput{
		Prepared: prepared, PrincipalID: 70, Purpose: PrincipalAvatarPurpose,
		Slot: "current", Name: "a.png", ActorID: 70,
	}, h.now)
	assert.ErrorIs(t, err, ErrAccountTransaction)
}

func TestAccountResourceAdoptsExistingObjectWithoutCopyingBytes(t *testing.T) {
	h := newAccountResourceHarness(t)
	key := "resources/adopted.png"
	_, err := h.driver.Put(t.Context(), key, bytes.NewBufferString("zzz"), 3)
	require.NoError(t, err)

	prepared, err := h.resource.AdoptOn(t.Context(), h.db, 70, key, h.now.Add(time.Hour))
	require.NoError(t, err)
	var binding *model.ResourceBinding
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		binding, err = h.resource.Create(t.Context(), tx, AccountResourceInput{
			Prepared: prepared, PrincipalID: 70, Purpose: PrincipalAvatarPurpose,
			Slot: "current", Name: "adopted.png", ActorID: 70,
		}, h.now)
		return err
	}))
	require.NotNil(t, binding)

	read, err := h.resource.Open(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	defer func() { require.NoError(t, read.Reader.Close()) }()
	body, err := io.ReadAll(read.Reader)
	require.NoError(t, err)
	assert.Equal(t, "zzz", string(body))

	// Adoption reuses the original key rather than writing a second copy.
	object, err := h.q.Object.WithContext(t.Context()).Where(h.q.Object.StorageKey.Eq(key)).Take()
	require.NoError(t, err)
	assert.Equal(t, "verified", object.State)
}

func TestCandidateSlotFromKeyAcceptsOnlyGeneratedCandidateNames(t *testing.T) {
	token := strings.Repeat("a", 48)
	slot, err := candidateSlotFromKey("avatars/70/" + token + ".png")
	require.NoError(t, err)
	assert.Equal(t, "candidate-"+token, slot)

	for name, key := range map[string]string{
		"not png":     "avatars/70/" + token + ".jpg",
		"short token": "avatars/70/" + strings.Repeat("a", 47) + ".png",
		"long token":  "avatars/70/" + strings.Repeat("a", 49) + ".png",
		"no base":     "",
		"directory":   "avatars/70/",
	} {
		_, err := candidateSlotFromKey(key)
		assert.ErrorIs(t, err, ErrInvalidKey, name)
	}
}

// seedLegacyAvatar writes the pre-resource-binding representation: a verified
// object plus an account reference naming its purpose.
func (h *accountResourceHarness) seedLegacyAvatar(t *testing.T, refID, objectID, principal guid.ID, purpose, key string, expires *int64) {
	t.Helper()
	require.NoError(t, h.q.Object.WithContext(t.Context()).Create(&model.Object{
		ID: objectID, BackendID: "local", StorageKey: key, URI: "file:///" + key,
		State: "verified", Size: 3, MIME: "image/png", Type: "image", Subtype: "png",
		MetadataJSON: "{}", MetadataState: "unknown", Version: 1, CreatedAt: h.now.UnixMilli(),
	}))
	require.NoError(t, h.q.AccountReference.WithContext(t.Context()).Create(&model.AccountReference{
		ID: refID, PrincipalID: principal, ObjectID: objectID, Purpose: purpose,
		State: "active", ExpiresAt: expires, CreatedAt: h.now.UnixMilli(),
	}))
}

func TestMigrateLegacyAvatarsRebindsCurrentAndCandidateReferences(t *testing.T) {
	h := newAccountResourceHarness(t)
	token := strings.Repeat("b", 48)
	future := h.now.Add(time.Hour).UnixMilli()
	past := h.now.Add(-time.Hour).UnixMilli()

	h.seedLegacyAvatar(t, 1001, 2001, 70, "avatar", "avatars/70/current.png", nil)
	h.seedLegacyAvatar(t, 1002, 2002, 70, "avatar_candidate", "avatars/70/"+token+".png", &future)
	h.seedLegacyAvatar(t, 1003, 2003, 70, "avatar_candidate", "avatars/70/"+strings.Repeat("c", 48)+".png", &past)

	require.NoError(t, h.resource.MigrateLegacyAvatars(t.Context(), h.db, h.now))

	// The live avatar becomes a current-slot binding and resolves by purpose.
	present, err := h.resource.Has(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	assert.True(t, present)

	// The still-valid candidate keeps the slot derived from its storage key.
	present, err = h.resource.Has(t.Context(), h.db, 70, PrincipalAvatarCandidatePurpose, "candidate-"+token, h.now)
	require.NoError(t, err)
	assert.True(t, present)

	state := func(id guid.ID) string {
		row, err := h.q.AccountReference.WithContext(t.Context()).Where(h.q.AccountReference.ID.Eq(id)).Take()
		require.NoError(t, err)
		return row.State
	}
	assert.Equal(t, "inactive", state(1001))
	assert.Equal(t, "inactive", state(1002))
	// An already-expired candidate is retired in place and never becomes a
	// binding, so a stale upload cannot come back as a promotable candidate.
	assert.Equal(t, "expired", state(1003))
	bindings, err := h.q.ResourceBinding.WithContext(t.Context()).
		Where(h.q.ResourceBinding.Purpose.Eq(PrincipalAvatarCandidatePurpose)).Find()
	require.NoError(t, err)
	assert.Len(t, bindings, 1)
}

func TestMigrateLegacyAvatarsIsRepeatableAndSkipsBoundSlots(t *testing.T) {
	h := newAccountResourceHarness(t)
	h.seedLegacyAvatar(t, 1001, 2001, 70, "avatar", "avatars/70/current.png", nil)

	require.NoError(t, h.resource.MigrateLegacyAvatars(t.Context(), h.db, h.now))
	first, err := h.q.ResourceBinding.WithContext(t.Context()).Count()
	require.NoError(t, err)

	// Re-running must not double-bind: the reference is already inactive, and
	// an interrupted run that left an active binding must not add a second.
	require.NoError(t, h.resource.MigrateLegacyAvatars(t.Context(), h.db, h.now))
	second, err := h.q.ResourceBinding.WithContext(t.Context()).Count()
	require.NoError(t, err)
	assert.Equal(t, first, second)

	h.seedLegacyAvatar(t, 1004, 2004, 70, "avatar", "avatars/70/again.png", nil)
	require.NoError(t, h.resource.MigrateLegacyAvatars(t.Context(), h.db, h.now))
	third, err := h.q.ResourceBinding.WithContext(t.Context()).Count()
	require.NoError(t, err)
	assert.Equal(t, second, third, "an already-bound current slot must not gain a duplicate binding")

	row, err := h.q.AccountReference.WithContext(t.Context()).Where(h.q.AccountReference.ID.Eq(guid.ID(1004))).Take()
	require.NoError(t, err)
	assert.Equal(t, "inactive", row.State)
}

func TestAccountResourceSurfacesIdentifierExhaustion(t *testing.T) {
	ids := &failingIDs{}
	ids.remaining.Store(2)
	h := newAccountResourceHarnessWithIDs(t, ids)
	// Staging consumes the budget, so the identifiers create needs for the
	// space, root, file, version and binding are unavailable. The failure has
	// to surface instead of committing a half-built tree.
	prepared, err := h.resource.StageOn(t.Context(), h.db, 70, "resources/a.png", bytes.NewBufferString("abc"), 3, h.now.Add(time.Hour))
	require.NoError(t, err)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, createErr := h.resource.Create(t.Context(), tx, AccountResourceInput{
			Prepared: prepared, PrincipalID: 70, Purpose: PrincipalAvatarPurpose,
			Slot: "current", Name: "a.png", ActorID: 70,
		}, h.now)
		return createErr
	})
	assert.ErrorIs(t, err, ErrUnsupported)

	present, err := h.resource.Has(t.Context(), h.db, 70, PrincipalAvatarPurpose, "current", h.now)
	require.NoError(t, err)
	assert.False(t, present)
}

func TestPurgeOrphanedRejectsUnusablePaging(t *testing.T) {
	h := newAccountResourceHarness(t)
	for name, args := range map[string][2]int{
		"negative cursor": {-1, 10},
		"zero limit":      {0, 0},
		"oversized limit": {0, 1001},
	} {
		next, err := h.resource.PurgeOrphaned(t.Context(), h.db, h.now, guid.ID(args[0]), args[1])
		assert.ErrorIs(t, err, ErrInvalidKey, name)
		assert.EqualValues(t, args[0], next, name)
	}
	var nilResource *AccountResourceFiles
	_, err := nilResource.PurgeOrphaned(t.Context(), h.db, h.now, 0, 10)
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestPurgeOrphanedRemovesOnlyFilesWithNoActiveBinding(t *testing.T) {
	h := newAccountResourceHarness(t)
	released := h.bind(t, 70, PrincipalAvatarPurpose, "current", "gone.png", "one", nil, false)
	kept := h.bind(t, 70, PrincipalAvatarPurpose, "backup", "kept.png", "two", nil, false)

	// Retire one slot only. Its node is now orphaned; the other is still live.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.resource.deactivateSlot(t.Context(), tx, 70, PrincipalAvatarPurpose, "current", h.now)
	}))

	_, err := h.resource.PurgeOrphaned(t.Context(), h.db, h.now, 0, 100)
	require.NoError(t, err)

	exists := func(id guid.ID) bool {
		count, err := h.q.Node.WithContext(t.Context()).Where(h.q.Node.ID.Eq(id)).Count()
		require.NoError(t, err)
		return count > 0
	}
	assert.False(t, exists(released.FileID), "a file with no active binding must be purged")
	assert.True(t, exists(kept.FileID), "a file still serving a slot must survive")

	// The purge is audited and takes the binding history with it.
	assert.Contains(t, h.eventTypes(), "file_resource_purged")
	history, err := h.q.ResourceBinding.WithContext(t.Context()).Where(h.q.ResourceBinding.FileID.Eq(released.FileID)).Count()
	require.NoError(t, err)
	assert.Zero(t, history)

	// The surviving file still reads back, so the purge did not disturb it.
	read, err := h.resource.Open(t.Context(), h.db, 70, PrincipalAvatarPurpose, "backup", h.now)
	require.NoError(t, err)
	defer func() { require.NoError(t, read.Reader.Close()) }()
	assert.Equal(t, "kept.png", read.Name)
}

func TestPurgeOrphanedIsIdempotentAndReportsItsCursor(t *testing.T) {
	h := newAccountResourceHarness(t)
	first := h.bind(t, 70, PrincipalAvatarPurpose, "a", "a.png", "one", nil, false)
	second := h.bind(t, 70, PrincipalAvatarPurpose, "b", "b.png", "two", nil, false)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.resource.Release(t.Context(), tx, 70, h.now)
	}))

	// A page smaller than the candidate set returns a non-zero cursor so the
	// caller knows to continue; a short page ends the sweep with zero.
	cursor, err := h.resource.PurgeOrphaned(t.Context(), h.db, h.now, 0, 1)
	require.NoError(t, err)
	assert.NotZero(t, cursor)

	cursor, err = h.resource.PurgeOrphaned(t.Context(), h.db, h.now, 0, 100)
	require.NoError(t, err)
	assert.Zero(t, cursor)

	for _, id := range []guid.ID{first.FileID, second.FileID} {
		count, err := h.q.Node.WithContext(t.Context()).Where(h.q.Node.ID.Eq(id)).Count()
		require.NoError(t, err)
		assert.Zero(t, count)
	}

	// Running again over an already-clean tree must be a no-op, not an error.
	cursor, err = h.resource.PurgeOrphaned(t.Context(), h.db, h.now, 0, 100)
	require.NoError(t, err)
	assert.Zero(t, cursor)
}
