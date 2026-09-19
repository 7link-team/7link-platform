package files

import (
	"io"
	"strings"
	"testing"

	"7link.com/platform/internal/files/query"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
)

func TestBasicFileLifecycleUsesPersonalRootAndImmutableContent(t *testing.T) {
	h := newOperationServiceHarness(t)

	root, err := h.service.ensureOwnedRoot(h.ctx, "personal")
	require.NoError(t, err)
	require.Equal(t, "directory", root.Kind)
	require.Equal(t, "personal", root.Scope)
	require.Equal(t, "Personal files", root.Name)
	require.Positive(t, root.ID)

	again, err := h.service.ensureOwnedRoot(h.ctx, "personal")
	require.NoError(t, err)
	require.Equal(t, root, again)
	h.assertAudit(t, 1)

	roots, err := h.service.listRoots(h.ctx, browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, roots.Items, 2)
	require.Equal(t, root, roots.Items[1])

	directory, err := h.service.createDirectory(h.ctx, root.ID, "Contracts")
	require.NoError(t, err)
	require.Equal(t, "directory", directory.Kind)
	require.Equal(t, int64(1), directory.Revision)

	payload := "signed agreement\n"
	uploaded, err := h.service.uploadFile(h.ctx, directory.ID, "Agreement.TXT", "text/plain", strings.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, "file", uploaded.Kind)
	require.Equal(t, "Agreement.TXT", uploaded.Name)
	require.Equal(t, "txt", uploaded.Extension)
	require.Equal(t, int64(1), uploaded.Version)
	require.Equal(t, int64(2), uploaded.Revision)
	require.NotNil(t, uploaded.Size)
	require.EqualValues(t, len(payload), *uploaded.Size)
	download, err := h.service.downloadFile(h.ctx, uploaded.ID)
	require.NoError(t, err)
	require.Equal(t, "Agreement.TXT", download.Name)
	require.EqualValues(t, len(payload), download.Size)
	content, err := io.ReadAll(download.Reader)
	require.NoError(t, err)
	require.NoError(t, download.Reader.Close())
	require.Equal(t, payload, string(content))

	renamed, err := h.service.renameNode(h.ctx, uploaded.ID, uploaded.Revision, "Final.txt")
	require.NoError(t, err)
	require.Equal(t, "Final.txt", renamed.Name)
	require.Equal(t, uploaded.Revision+1, renamed.Revision)
	require.Equal(t, uploaded.Version, renamed.Version)
	_, err = h.service.renameNode(h.ctx, uploaded.ID, uploaded.Revision, "stale.txt")
	require.ErrorIs(t, err, errTreeConflict)

	trashed, err := h.service.trashNode(h.ctx, uploaded.ID, renamed.Revision)
	require.NoError(t, err)
	require.Positive(t, trashed.BatchID)
	children, err := h.service.listChildren(h.ctx, directory.ID, browserPageRequest{})
	require.NoError(t, err)
	require.Empty(t, children.Items)
	require.Equal(t, "Personal files", children.Path[0].Name)
	_, err = h.service.downloadFile(h.ctx, uploaded.ID)
	require.ErrorIs(t, err, ErrNotFound)
	renamedDirectory, err := h.service.renameNode(h.ctx, directory.ID, directory.Revision, "Agreements")
	require.NoError(t, err)
	require.Equal(t, "directory", renamedDirectory.Kind)
	_, err = h.service.trashNode(h.ctx, directory.ID, renamedDirectory.Revision)
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
	h.assertAudit(t, 8)
}

func TestBasicFileLifecycleRejectsConflictsAndOversizedUpload(t *testing.T) {
	h := newOperationServiceHarness(t)
	root, err := h.service.ensureOwnedRoot(h.ctx, "personal")
	require.NoError(t, err)

	first, err := h.service.createDirectory(h.ctx, root.ID, "Reports")
	require.NoError(t, err)
	_, err = h.service.createDirectory(h.ctx, root.ID, "reports")
	require.ErrorIs(t, err, errTreeConflict)

	h.service.maxUploadBytes = 2
	_, err = h.service.uploadFile(h.ctx, first.ID, "large.txt", "text/plain", strings.NewReader("123"), 3)
	require.ErrorIs(t, err, errOperationInput)
	q := query.Bind(h.db, h.tree.prefix)
	preparations, err := q.AccountPreparation.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Empty(t, preparations, "size refusal happens before storage preparation")
}

func TestAccountFileRootIsLazyIdempotentAndIsolatedFromTenants(t *testing.T) {
	h := newOperationServiceHarness(t)
	accountContext := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70})

	before, err := h.service.listRoots(accountContext, browserPageRequest{})
	require.NoError(t, err)
	require.Empty(t, before.Items, "GET must not create an account root")

	root, err := h.service.ensureOwnedRoot(accountContext, "account")
	require.NoError(t, err)
	require.Equal(t, "account", root.Scope)
	require.Equal(t, "Account files", root.Name)
	requireOptionalID(t, root.PrincipalID, 70)
	require.Nil(t, root.TenantID)
	again, err := h.service.ensureOwnedRoot(accountContext, "account")
	require.NoError(t, err)
	require.Equal(t, root, again)

	accountRoots, err := h.service.listRoots(accountContext, browserPageRequest{})
	require.NoError(t, err)
	require.Equal(t, []fileEntryView{root}, accountRoots.Items)
	_, err = h.service.getFileEntry(h.ctx, root.ID)
	require.ErrorIs(t, err, errOperationForbidden, "tenant context cannot read account files by global ID")
	_, err = h.service.getFileEntry(accountContext, 2)
	require.ErrorIs(t, err, errOperationForbidden, "account context cannot read tenant files by global ID")
	_, err = h.service.ensureOwnedRoot(accountContext, "personal")
	require.ErrorIs(t, err, errOperationForbidden)
	_, err = h.service.ensureOwnedRoot(h.ctx, "account")
	require.ErrorIs(t, err, errOperationForbidden)

	otherAccount := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 71})
	otherRoots, err := h.service.listRoots(otherAccount, browserPageRequest{})
	require.NoError(t, err)
	require.Empty(t, otherRoots.Items)
	_, err = h.service.getFileEntry(otherAccount, root.ID)
	require.ErrorIs(t, err, errOperationForbidden)
}
