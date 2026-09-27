package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWritableAccountViewFileMetadataRecordsOperator(t *testing.T) {
	h := newOperationServiceHarness(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70, AccountView: &authn.AccountViewIdentity{ID: 1, ActorID: 71, ScopeType: "personal", ScopeID: 70}})
	root, err := h.service.ensureOwnedRoot(ctx)
	require.NoError(t, err)
	file, err := h.service.uploadFile(ctx, root.ID, "view.txt", "text/plain", strings.NewReader("abc"), 3)
	require.NoError(t, err)
	q := query.Bind(h.db, h.tree.prefix)
	version, err := q.Version.WithContext(ctx).Where(q.Version.NodeID.Eq(file.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, guid.ID(71), version.CreatedBy)
	acl, err := q.NodeACL.WithContext(ctx).Where(q.NodeACL.NodeID.Eq(root.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, guid.ID(70), acl.SubjectID)
	require.Equal(t, guid.ID(71), acl.CreatedBy)
	trashed, err := h.service.trashNode(ctx, file.ID, file.Revision)
	require.NoError(t, err)
	batch, err := q.TrashBatch.WithContext(ctx).Where(q.TrashBatch.ID.Eq(trashed.BatchID)).Take()
	require.NoError(t, err)
	require.Equal(t, guid.ID(71), batch.DeletedBy)
	node, err := q.Node.WithContext(ctx).Where(q.Node.ID.Eq(file.ID)).Take()
	require.NoError(t, err)
	_, err = h.service.restoreNode(ctx, file.ID, node.Version)
	require.NoError(t, err)
	batch, err = q.TrashBatch.WithContext(ctx).Where(q.TrashBatch.ID.Eq(trashed.BatchID)).Take()
	require.NoError(t, err)
	require.NotNil(t, batch.RestoredBy)
	require.Equal(t, guid.ID(71), *batch.RestoredBy)
}

func TestBasicFileLifecycleUsesTheOwnRootAndImmutableContent(t *testing.T) {
	h := newOperationServiceHarness(t)

	root, err := h.service.ensureOwnedRoot(h.ownCtx)
	require.NoError(t, err)
	require.Equal(t, "directory", root.Kind)
	require.Equal(t, "account", root.Scope)
	require.Equal(t, "Account files", root.Name)
	require.Positive(t, root.ID)

	again, err := h.service.ensureOwnedRoot(h.ownCtx)
	require.NoError(t, err)
	require.Equal(t, root, again)
	// The caller's own space belongs to no tenant, so the record is kept
	// against none. Read it from the table rather than through the tenant
	// filter, which is built to answer "what happened in tenant X".
	var ownEvents int64
	require.NoError(t, h.db.Table(h.tree.prefix+"iam_audit_events").
		Where("event_type = ?", "file_root_created").Count(&ownEvents).Error)
	require.Equal(t, int64(1), ownEvents)

	// The tenant's own collection is a different question and a different
	// space: creating the caller's root adds nothing to it.
	roots, err := h.service.listRoots(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, roots.Items, 1)
	require.Equal(t, "tenant", roots.Items[0].Scope)

	directory, err := h.service.createDirectory(h.ownCtx, root.ID, "Contracts")
	require.NoError(t, err)
	require.Equal(t, "directory", directory.Kind)
	require.Equal(t, int64(1), directory.Revision)

	payload := "signed agreement\n"
	uploaded, err := h.service.uploadFile(h.ownCtx, directory.ID, "Agreement.TXT", "text/plain", strings.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, "file", uploaded.Kind)
	require.Equal(t, "Agreement.TXT", uploaded.Name)
	require.Equal(t, "txt", uploaded.Extension)
	require.Equal(t, int64(1), uploaded.Version)
	require.Equal(t, int64(2), uploaded.Revision)
	require.NotNil(t, uploaded.Size)
	require.EqualValues(t, len(payload), *uploaded.Size)
	download, err := h.service.downloadFile(h.ownCtx, uploaded.ID)
	require.NoError(t, err)
	require.Equal(t, "Agreement.TXT", download.Name)
	require.EqualValues(t, len(payload), download.Size)
	content, err := io.ReadAll(download.Reader)
	require.NoError(t, err)
	require.NoError(t, download.Reader.Close())
	require.Equal(t, payload, string(content))

	renamed, err := h.service.renameNode(h.ownCtx, uploaded.ID, uploaded.Revision, "Final.txt")
	require.NoError(t, err)
	require.Equal(t, "Final.txt", renamed.Name)
	require.Equal(t, uploaded.Revision+1, renamed.Revision)
	require.Equal(t, uploaded.Version, renamed.Version)
	_, err = h.service.renameNode(h.ownCtx, uploaded.ID, uploaded.Revision, "stale.txt")
	require.ErrorIs(t, err, errTreeConflict)

	trashed, err := h.service.trashNode(h.ownCtx, uploaded.ID, renamed.Revision)
	require.NoError(t, err)
	require.Positive(t, trashed.BatchID)
	children, err := h.service.listChildren(h.ownCtx, directory.ID, browserPageRequest{})
	require.NoError(t, err)
	require.Empty(t, children.Items)
	require.Equal(t, "Account files", children.Path[0].Name)
	_, err = h.service.downloadFile(h.ownCtx, uploaded.ID)
	require.ErrorIs(t, err, ErrNotFound)
	renamedDirectory, err := h.service.renameNode(h.ownCtx, directory.ID, directory.Revision, "Agreements")
	require.NoError(t, err)
	require.Equal(t, "directory", renamedDirectory.Kind)
	_, err = h.service.trashNode(h.ownCtx, directory.ID, renamedDirectory.Revision)
	require.NoError(t, err)

	q := query.Bind(h.db, h.tree.prefix)
	preparations, err := q.AccountPreparation.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, preparations, 1)
	require.Equal(t, "attached", preparations[0].State)
	object, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(preparations[0].ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "verified", object.State)
	require.EqualValues(t, len(payload), object.Size)
	require.NoError(t, h.db.Table(h.tree.prefix+"iam_audit_events").
		Where("tenant_id = ?", 0).Count(&ownEvents).Error)
	require.Equal(t, int64(8), ownEvents, "every step in the caller's own space is recorded against no tenant")
}

func TestBasicFileLifecycleRejectsConflictsAndOversizedUpload(t *testing.T) {
	h := newOperationServiceHarness(t)
	root, err := h.service.ensureOwnedRoot(h.ownCtx)
	require.NoError(t, err)

	first, err := h.service.createDirectory(h.ownCtx, root.ID, "Reports")
	require.NoError(t, err)
	_, err = h.service.createDirectory(h.ownCtx, root.ID, "reports")
	require.ErrorIs(t, err, errTreeConflict)

	h.service.maxUploadBytes = 2
	_, err = h.service.uploadFile(h.ownCtx, first.ID, "large.txt", "text/plain", strings.NewReader("123"), 3)
	require.ErrorIs(t, err, errOperationInput)
	q := query.Bind(h.db, h.tree.prefix)
	preparations, err := q.AccountPreparation.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Empty(t, preparations, "size refusal happens before storage preparation")
}

func TestAccountFileRootIsLazyIdempotentAndIsolatedFromTenants(t *testing.T) {
	h := newOperationServiceHarness(t)
	accountContext := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70})

	before, err := h.service.listRoots(accountContext, testFileCollection(accountContext), browserPageRequest{})
	require.NoError(t, err)
	require.Empty(t, before.Items, "GET must not create an account root")

	root, err := h.service.ensureOwnedRoot(accountContext)
	require.NoError(t, err)
	require.Equal(t, "account", root.Scope)
	require.Equal(t, "Account files", root.Name)
	requireOptionalID(t, root.PrincipalID, 70)
	require.Nil(t, root.TenantID)
	again, err := h.service.ensureOwnedRoot(accountContext)
	require.NoError(t, err)
	require.Equal(t, root, again)

	accountRoots, err := h.service.listRoots(accountContext, testFileCollection(accountContext), browserPageRequest{})
	require.NoError(t, err)
	require.Equal(t, []fileEntryView{root}, accountRoots.Items)
	_, err = h.service.getFileEntry(h.ctx, root.ID)
	require.ErrorIs(t, err, errOperationForbidden, "tenant context cannot read account files by global ID")
	_, err = h.service.getFileEntry(accountContext, 2)
	require.ErrorIs(t, err, errOperationForbidden, "account context cannot read tenant files by global ID")
	// The service refuses a context that still carries a tenant: a question
	// about the caller's own space that names one is malformed. The route drops
	// the selection before calling, which personalContext's tests cover.
	_, err = h.service.ensureOwnedRoot(h.ctx)
	require.ErrorIs(t, err, errOperationForbidden)

	otherAccount := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 71})
	otherRoots, err := h.service.listRoots(otherAccount, testFileCollection(otherAccount), browserPageRequest{})
	require.NoError(t, err)
	require.Empty(t, otherRoots.Items)
	_, err = h.service.getFileEntry(otherAccount, root.ID)
	require.ErrorIs(t, err, errOperationForbidden)
}

func TestRootDisplayNameLabelsEveryRootScope(t *testing.T) {
	scoped := func(scope string) *model.Node { return &model.Node{RootScope: &scope} }

	// Roots carry no stored name, so the browser labels them by what they are.
	// Every scope the tree can produce needs its own label, otherwise a root
	// would render blank or as a raw scope token.
	for scope, want := range map[string]string{
		"account":    "Account files",
		"tenant":     "Tenant files",
		"entity":     "Entity files",
		"department": "Department files",
	} {
		assert.Equal(t, want, rootDisplayName(scoped(scope)), scope)
	}

	// A scope the labeller does not know must not invent a label; it falls back
	// to the node's own naming so an unlabelled root still renders something.
	assert.Equal(t, "workspace", rootDisplayName(scoped("workspace")))
	assert.Equal(t, "reports", rootDisplayName(&model.Node{Name: "reports"}))
	assert.Empty(t, rootDisplayName(&model.Node{}))
	assert.Empty(t, rootDisplayName(nil))
}

func TestBasicClaimsRequiresACompletelyWiredUploadPath(t *testing.T) {
	h := newOperationServiceHarness(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70})

	ready, err := h.service.basicClaims(ctx)
	require.NoError(t, err)
	require.NotNil(t, ready)

	// Every collaborator below is required to complete an upload or download.
	// Missing any one of them must be refused up front rather than surfacing
	// as a nil dereference partway through a file write.
	for name, break_ := range map[string]func(s *operationService){
		"no ids":       func(s *operationService) { s.ids = nil },
		"no appender":  func(s *operationService) { s.appender = nil },
		"no objects":   func(s *operationService) { s.objects = nil },
		"no driver":    func(s *operationService) { s.driver = nil },
		"no timeout":   func(s *operationService) { s.uploadTimeout = 0 },
		"no max bytes": func(s *operationService) { s.maxUploadBytes = 0 },
	} {
		broken := *h.service
		break_(&broken)
		claims, err := broken.basicClaims(ctx)
		assert.ErrorIs(t, err, errOperationUnavailable, name)
		assert.Nil(t, claims, name)
	}

	// An unauthenticated caller is rejected before any of that is considered.
	_, err = h.service.basicClaims(t.Context())
	assert.Error(t, err)
}

// TestBasicAndBrowserEntryPointsSurfaceStorageFailures drops the tables the
// file tree is read from and walks every entry point. A storage outage has to
// come back as an error from all of them: an entry point that swallowed the
// fault would answer with an empty listing or a missing file, telling the
// caller their data is gone when in fact it was never read.
func TestBasicAndBrowserEntryPointsSurfaceStorageFailures(t *testing.T) {
	h := newOperationServiceHarness(t)

	root, err := h.service.ensureOwnedRoot(h.ownCtx)
	require.NoError(t, err)
	uploaded, err := h.service.uploadFile(h.ownCtx, root.ID, "report.txt", "text/plain", strings.NewReader("abc"), 3)
	require.NoError(t, err)
	directory, err := h.service.createDirectory(h.ownCtx, root.ID, "archive")
	require.NoError(t, err)

	// A positive control first: every path below works before the outage, so a
	// later error is attributable to the dropped tables and not to bad input.
	_, err = h.service.listChildren(h.ownCtx, root.ID, browserPageRequest{Limit: 10})
	require.NoError(t, err)

	// The closure table backs every tree walk; the trash roots back the recycle
	// bin listing, which does not walk the tree.
	for _, table := range []string{"file_node_closure", "file_trash_roots"} {
		require.NoError(t, h.db.Exec("DROP TABLE "+h.tree.prefix+table).Error, table)
	}

	collection := testFileCollection(h.ownCtx)
	for name, call := range map[string]func() error{
		"list roots": func() error {
			_, err := h.service.listRoots(h.ownCtx, collection, browserPageRequest{Limit: 10})
			return err
		},
		"list trash": func() error {
			_, err := h.service.listTrash(h.ownCtx, collection, browserPageRequest{Limit: 10})
			return err
		},
		"list children": func() error {
			_, err := h.service.listChildren(h.ownCtx, root.ID, browserPageRequest{Limit: 10})
			return err
		},
		"read entry": func() error {
			_, err := h.service.readFileEntry(h.ownCtx, root.ID, "report.txt")
			return err
		},
		"ensure root": func() error {
			_, err := h.service.ensureOwnedRoot(h.ownCtx)
			return err
		},
		"create directory": func() error {
			_, err := h.service.createDirectory(h.ownCtx, root.ID, "another")
			return err
		},
		"upload": func() error {
			_, err := h.service.uploadFile(h.ownCtx, root.ID, "second.txt", "text/plain", strings.NewReader("xyz"), 3)
			return err
		},
		"download": func() error {
			_, err := h.service.downloadFile(h.ownCtx, uploaded.ID)
			return err
		},
		"trash": func() error {
			_, err := h.service.trashNode(h.ownCtx, directory.ID, directory.Revision)
			return err
		},
		"restore": func() error {
			_, err := h.service.restoreNode(h.ownCtx, directory.ID, directory.Revision)
			return err
		},
		"restore as": func() error {
			_, err := h.service.restoreNodeAs(h.ownCtx, directory.ID, directory.Revision, "renamed")
			return err
		},
		"mutate": func() error {
			return h.service.mutateNode(h.ownCtx, directory.ID, "rename",
				func(context.Context, *gorm.DB, *authn.Claims, directoryTree, *model.Node, *nodeAuthorizer) error {
					return nil
				})
		},
	} {
		assert.Error(t, call(), "%s must report the storage failure", name)
	}
}

// TestFileOperationsFailClosedWhenPermissionsAreUnreadable removes the ACL
// store and walks the authorized entry points. Every one must refuse. A path
// that fell through to "allow" because it could not read the rules would turn
// a storage fault into an authorization bypass, which is strictly worse than
// an outage.
func TestFileOperationsFailClosedWhenPermissionsAreUnreadable(t *testing.T) {
	h := newOperationServiceHarness(t)

	root, err := h.service.ensureOwnedRoot(h.ownCtx)
	require.NoError(t, err)
	directory, err := h.service.createDirectory(h.ownCtx, root.ID, "archive")
	require.NoError(t, err)
	uploaded, err := h.service.uploadFile(h.ownCtx, root.ID, "report.txt", "text/plain", strings.NewReader("abc"), 3)
	require.NoError(t, err)

	// Positive control: these succeed while the rules are readable.
	_, err = h.service.downloadFile(h.ownCtx, uploaded.ID)
	require.NoError(t, err)

	require.NoError(t, h.db.Exec("DROP TABLE "+h.tree.prefix+"file_node_acl").Error)

	for name, call := range map[string]func() error{
		"list children": func() error {
			_, err := h.service.listChildren(h.ownCtx, root.ID, browserPageRequest{Limit: 10})
			return err
		},
		"read entry": func() error {
			_, err := h.service.readFileEntry(h.ownCtx, root.ID, "report.txt")
			return err
		},
		"download": func() error {
			_, err := h.service.downloadFile(h.ownCtx, uploaded.ID)
			return err
		},
		"create directory": func() error {
			_, err := h.service.createDirectory(h.ownCtx, root.ID, "another")
			return err
		},
		"upload": func() error {
			_, err := h.service.uploadFile(h.ownCtx, root.ID, "second.txt", "text/plain", strings.NewReader("xyz"), 3)
			return err
		},
		"trash": func() error {
			_, err := h.service.trashNode(h.ownCtx, directory.ID, directory.Revision)
			return err
		},
		"mutate": func() error {
			return h.service.mutateNode(h.ownCtx, directory.ID, "rename",
				func(context.Context, *gorm.DB, *authn.Claims, directoryTree, *model.Node, *nodeAuthorizer) error {
					return nil
				})
		},
	} {
		assert.Error(t, call(), "%s must fail closed when the ACL store is unreadable", name)
	}
}

// TestFileEntryPointsPropagateEveryStatementFailure fails the Nth statement
// against each table and re-runs each entry point. Every reached failure has to
// surface.
//
// This is the guard-by-guard counterpart to the table-drop sweeps above. Those
// prove the first access to a table is checked; this walks deeper, so a later
// query whose error was assigned and then ignored - the classic `_ = err` or a
// shadowed err inside a loop - shows up as an entry point that returned success
// while a statement underneath it failed.
//
// A case whose fault never fired is skipped rather than asserted: that entry
// point simply never made that many statements against that table, and claiming
// a guard it never reached would be a false assertion.
func TestFileEntryPointsPropagateEveryStatementFailure(t *testing.T) {
	tables := []string{
		"file_nodes", "file_node_closure", "file_node_acl", "file_spaces",
		"file_versions", "file_objects", "file_trash_roots", "file_trash_batches",
		"file_operations", "file_resource_bindings",
	}

	type entryPoint struct {
		name string
		call func(t *testing.T, h operationServiceHarness) error
	}
	entryPoints := []entryPoint{
		{"list roots", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.listRoots(h.ownCtx, testFileCollection(h.ownCtx), browserPageRequest{Limit: 10})
			return err
		}},
		{"list trash", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.listTrash(h.ownCtx, testFileCollection(h.ownCtx), browserPageRequest{Limit: 10})
			return err
		}},
		{"list children", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.listChildren(h.ownCtx, 10, browserPageRequest{Limit: 10})
			return err
		}},
		{"read entry", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.readFileEntry(h.ownCtx, 2, "child.txt")
			return err
		}},
		{"ensure root", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.ensureOwnedRoot(h.ownCtx)
			return err
		}},
		{"create directory", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.createDirectory(h.ownCtx, 10, "made-here")
			return err
		}},
		{"upload", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.uploadFile(h.ownCtx, 10, "uploaded.txt", "text/plain", strings.NewReader("abc"), 3)
			return err
		}},
		{"download", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.downloadFile(h.ownCtx, 32)
			return err
		}},
		{"trash", func(t *testing.T, h operationServiceHarness) error {
			_, err := h.service.trashNode(h.ownCtx, 32, treeNode(t, h.db, h.tree.prefix, 32).Version)
			return err
		}},
		{"restore", func(t *testing.T, h operationServiceHarness) error {
			node := treeNode(t, h.db, h.tree.prefix, 32)
			if _, err := h.service.trashNode(h.ownCtx, 32, node.Version); err != nil {
				return err
			}
			_, err := h.service.restoreNode(h.ownCtx, 32, treeNode(t, h.db, h.tree.prefix, 32).Version)
			return err
		}},
		{"rename", func(t *testing.T, h operationServiceHarness) error {
			return h.service.mutateNode(h.ownCtx, 32, "rename",
				func(context.Context, *gorm.DB, *authn.Claims, directoryTree, *model.Node, *nodeAuthorizer) error {
					return nil
				})
		}},
		{"immediate operation", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.run(h.ownCtx, "fault-sweep", h.input)
			return err
		}},
	}

	for _, point := range entryPoints {
		reached := 0
		for _, table := range tables {
			for skip := int64(0); skip < 12; skip++ {
				name := fmt.Sprintf("%s/%s/skip%d", point.name, table, skip)
				t.Run(name, func(t *testing.T) {
					h := newOperationServiceHarness(t)
					fault := failTableAfter(t, h.service.runtime.Database().Write(), table, skip)
					err := point.call(t, h)
					if !fault.Fired() {
						t.Skipf("%s never made %d statements against %s", point.name, skip+1, table)
					}
					reached++
					assert.Error(t, err, "%s swallowed a failed statement against %s", point.name, table)
				})
			}
		}
		// Skipping a combination an entry point never reaches is correct, but
		// skipping all of them is not: it would mean this entry point stopped
		// touching the database at these depths and the sweep quietly stopped
		// asserting anything about it. Without this the whole test could decay
		// into green skips after a refactor.
		assert.NotZero(t, reached, "%s reached no injected failure; the sweep no longer covers it", point.name)
	}
}

// TestDownloadRefusesToServeBytesItCannotRecord pins the rule that a download
// and its record land together.
//
// The reader is opened before the event is written, so there is a moment where
// the bytes are available and the record is not. If the write fails the bytes
// must not be handed back: the caller would have the file and the audit would
// not know, which is the one outcome an immutable record exists to prevent.
// Closing the reader on that path also keeps a failed download from leaking the
// handle.
func TestDownloadRefusesToServeBytesItCannotRecord(t *testing.T) {
	h := newOperationServiceHarness(t)
	root, err := h.service.ensureOwnedRoot(h.ownCtx)
	require.NoError(t, err)
	payload := "ledger\n"
	uploaded, err := h.service.uploadFile(h.ownCtx, root.ID, "ledger.txt", "text/plain", strings.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)

	readable, err := h.service.downloadFile(h.ownCtx, uploaded.ID)
	require.NoError(t, err, "the download works before the record is made to fail")
	require.NoError(t, readable.Reader.Close())

	recordFailure := errors.New("audit unavailable")
	appendEvent := h.service.appender
	h.service.appender = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "file_downloaded" {
			return recordFailure
		}
		return appendEvent(ctx, tx, event)
	}
	refused, err := h.service.downloadFile(h.ownCtx, uploaded.ID)
	require.ErrorIs(t, err, recordFailure)
	require.Nil(t, refused.Reader, "a download that cannot be recorded hands back nothing")
	require.Zero(t, refused.Size)
}

// TestFileWritesFailWholeWhenIDsRunOut starves each write of identifiers at
// every allocation it makes. Each starved run must fail and leave the tree,
// the stored versions and the audit record exactly as they were: a write that
// had created its node but not its version, or its version but not its
// record, would be a half-applied change nobody asked for.
func TestFileWritesFailWholeWhenIDsRunOut(t *testing.T) {
	type write struct {
		name string
		call func(t *testing.T, h operationServiceHarness) error
	}
	writes := []write{
		{"ensure root", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.ensureOwnedRoot(h.ownCtx)
			return err
		}},
		{"create directory", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.createDirectory(h.ctx, 2, "made-here")
			return err
		}},
		{"upload", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.uploadFile(h.ctx, 2, "uploaded.txt", "text/plain", strings.NewReader("abc"), 3)
			return err
		}},
		{"trash", func(t *testing.T, h operationServiceHarness) error {
			_, err := h.service.trashNode(h.ctx, 32, treeNode(t, h.db, h.tree.prefix, 32).Version)
			return err
		}},
		{"immediate operation", func(_ *testing.T, h operationServiceHarness) error {
			_, err := h.service.run(h.ctx, "starved", h.input)
			return err
		}},
	}
	// Staged upload bytes are deliberately left to expire, so the snapshot is
	// the tree, its versions and the audit record rather than every table.
	snapshot := func(t *testing.T, h operationServiceHarness) [4]int64 {
		t.Helper()
		q := query.Bind(h.db, h.tree.prefix)
		nodes, err := q.Node.WithContext(t.Context()).Count()
		require.NoError(t, err)
		versions, err := q.Version.WithContext(t.Context()).Count()
		require.NoError(t, err)
		_, own, err := h.audit.List(t.Context(), audit.Filter{Limit: 1})
		require.NoError(t, err)
		_, tenant, err := h.audit.List(t.Context(), audit.Filter{TenantID: 90, Limit: 1})
		require.NoError(t, err)
		return [4]int64{nodes, versions, own, tenant}
	}
	for _, point := range writes {
		completed := false
		for remaining := int64(0); remaining < 24 && !completed; remaining++ {
			t.Run(fmt.Sprintf("%s/ids%d", point.name, remaining), func(t *testing.T) {
				h := newOperationServiceHarness(t)
				before := snapshot(t, h)
				ids := &failingIDs{}
				ids.remaining.Store(remaining)
				ids.next.Store(20000)
				h.service.ids = ids
				err := point.call(t, h)
				if err == nil {
					completed = true
					return
				}
				require.ErrorIs(t, err, ErrUnsupported, "the allocator failure is what surfaces")
				require.Equal(t, before, snapshot(t, h), "a starved %s left a partial change", point.name)
			})
		}
		require.True(t, completed, "%s never completed; the starvation sweep no longer reaches its success path", point.name)
	}
}

func TestFileWritesRefuseStaleOrMalformedRequestsWithoutChangingTheTree(t *testing.T) {
	h := newOperationServiceHarness(t)
	before := immediateDatabaseSnapshot(t, h.db, h.tree)

	_, err := h.service.restoreNodeAs(h.ctx, 0, 1, "")
	require.ErrorIs(t, err, errOperationInput)
	_, err = h.service.restoreNodeAs(h.ctx, 32, 0, "")
	require.ErrorIs(t, err, errOperationInput)
	err = h.service.mutateNode(h.ctx, 0, "rename", func(context.Context, *gorm.DB, *authn.Claims, directoryTree, *model.Node, *nodeAuthorizer) error {
		return nil
	})
	require.ErrorIs(t, err, errOperationInput)
	_, err = h.service.uploadFile(h.ctx, 2, "bad/name.txt", "text/plain", strings.NewReader("x"), 1)
	require.Error(t, err, "a name that is not portable is refused")
	_, err = h.service.downloadFile(h.ctx, 10)
	require.ErrorIs(t, err, ErrNotFound, "a directory has no content to download")
	budget := h.service.budget
	h.service.budget.Nodes = 1
	_, err = h.service.trashNode(h.ctx, 10, treeNode(t, h.db, h.tree.prefix, 10).Version)
	require.ErrorIs(t, err, errHierarchy, "a subtree over the node budget is not trashed in part")
	h.service.budget = budget
	require.Equal(t, before, immediateDatabaseSnapshot(t, h.db, h.tree))

	file := treeNode(t, h.db, h.tree.prefix, 32)
	_, err = h.service.trashNode(h.ctx, 32, file.Version)
	require.NoError(t, err)
	trashed := treeNode(t, h.db, h.tree.prefix, 32)
	_, err = h.service.restoreNode(h.ctx, 32, trashed.Version+1)
	require.ErrorIs(t, err, errTreeConflict, "a restore against a stale revision is refused")
	withoutRecord := *h.service
	withoutRecord.appender = nil
	_, err = withoutRecord.restoreNode(h.ctx, 32, trashed.Version)
	require.ErrorIs(t, err, errOperationUnavailable, "a restore that cannot be recorded does not run")
	require.Equal(t, "trashed", treeNode(t, h.db, h.tree.prefix, 32).State)
	restored, err := h.service.restoreNode(h.ctx, 32, trashed.Version)
	require.NoError(t, err)
	require.Equal(t, "root.txt", restored.Name)
}

func TestEnsureOwnedRootIsNotCreatedWithoutItsAuditRecord(t *testing.T) {
	h := newOperationServiceHarness(t)
	recordFailure := errors.New("audit unavailable")
	h.service.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return recordFailure }
	_, err := h.service.ensureOwnedRoot(h.ownCtx)
	require.ErrorIs(t, err, recordFailure)
	q := query.Bind(h.db, h.tree.prefix)
	count, err := q.Space.WithContext(t.Context()).Where(q.Space.ScopeKind.Eq(spaceAccount)).Count()
	require.NoError(t, err)
	require.Zero(t, count, "an unrecorded personal root was rolled back")
}
