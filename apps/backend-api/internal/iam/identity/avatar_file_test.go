package identity

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"7link.com/platform/internal/files"
	filequery "7link.com/platform/internal/files/query"
	identityquery "7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

type avatarReadTestDriver struct {
	files.Driver
	reader io.ReadCloser
}

func (d avatarReadTestDriver) Open(context.Context, string) (io.ReadCloser, error) {
	return d.reader, nil
}

type avatarReadTestCloser struct {
	io.Reader
	closeErr error
	closed   bool
}

func (r *avatarReadTestCloser) Close() error {
	r.closed = true
	return r.closeErr
}

func TestAvatarReadClosesStorageAndPreservesFailures(t *testing.T) {
	readErr, closeErr := errors.New("avatar read failed"), errors.New("avatar close failed")
	for _, scenario := range []struct {
		name             string
		reader           io.Reader
		closeErr, wanted error
	}{
		{"success", strings.NewReader("avatar"), nil, nil},
		{"read", iotest.ErrReader(readErr), nil, readErr},
		{"close", strings.NewReader("avatar"), closeErr, closeErr},
		{"both", iotest.ErrReader(readErr), closeErr, readErr},
		{"oversized", strings.NewReader(strings.Repeat("x", 2*1024*1024+1)), nil, ErrInvalid},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reader := &avatarReadTestCloser{Reader: scenario.reader, closeErr: scenario.closeErr}
			repo := &Repository{storage: avatarReadTestDriver{reader: reader}}
			pixels, err := repo.readAvatarFile(t.Context(), 1, "test.png")
			require.True(t, reader.closed)
			if scenario.wanted == nil {
				require.NoError(t, err)
				require.Equal(t, "avatar", string(pixels))
			} else {
				require.ErrorIs(t, err, scenario.wanted)
			}
			if scenario.closeErr != nil {
				require.ErrorIs(t, err, scenario.closeErr)
			}
		})
	}
}

func TestAvatarStorageNameAcceptsBothLegacyRoutes(t *testing.T) {
	id := guid.ID(37851881703211008)
	name := "68625e4cdbcd54ebc46661809742b09baa40885ac96144d6.png"
	for _, prefix := range []string{"/api/avatars/", "/api/iam/avatars/"} {
		actual, ok := avatarStorageName(id, &Avatar{URL: prefix + id.String() + "/" + name})
		require.True(t, ok)
		require.Equal(t, name, actual)
	}
	for _, url := range []string{
		"/api/principals/" + id.String() + "/avatar",
		"/api/iam/avatars/999/" + name,
		"/api/iam/avatars/" + id.String() + "/../" + name,
		"/api/iam/avatars/" + id.String() + "/invalid.png",
	} {
		_, ok := avatarStorageName(id, &Avatar{URL: url})
		require.False(t, ok, url)
	}
}

func TestAvatarObjectsCandidatesSelectionAndExpirySQLite(t *testing.T) {
	h := newIdentityHarness(t, "avatars_")
	h.seedTenant(t, 7, "active")
	principal, err := h.service.Create(t.Context(), 7, "avatar-user", "VerySecurePassword123!", "Avatar user", "")
	require.NoError(t, err)
	first, err := h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{Style: "pixel"})
	require.NoError(t, err)
	q := filequery.Bind(h.db, h.prefix)
	object, err := q.Object.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "verified", object.State)
	require.Equal(t, "image/png", object.MIME)
	require.Equal(t, 256, *object.Width)
	require.Equal(t, 256, *object.Height)
	require.Contains(t, object.URI, "file:///")
	require.Equal(t, "/api/principals/"+principal.ID.String()+"/avatar", first.URL)
	name, ok := avatarStorageName(principal.ID, storedAvatar(h.principal(t, principal.ID).Avatar))
	require.True(t, ok)
	pixels, err := h.repository.readAvatarFile(t.Context(), principal.ID, name)
	require.NoError(t, err)
	digests := files.NewChecksums()
	_, err = digests.Write(pixels)
	require.NoError(t, err)
	require.Equal(t, digests.Digests().SHA512, object.SHA512)
	candidates, err := h.service.AvatarCandidates(t.Context(), principal.ID, "robot")
	require.NoError(t, err)
	require.Len(t, candidates, 6)
	chosen := candidates[2]
	selected, err := h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{CandidateURL: chosen.URL, Style: chosen.Style})
	require.NoError(t, err)
	require.Equal(t, chosen.Style, selected.Style, "selection must reuse the candidate object")
	require.Equal(t, "/api/principals/"+principal.ID.String()+"/avatar", selected.URL)
	count, err := q.Object.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 7, count, "candidate selection must not copy bytes")
	_, err = h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{CandidateURL: chosen.URL, Style: chosen.Style})
	require.ErrorIs(t, err, ErrNotFound, "a consumed candidate cannot be reused as a candidate")
	_, err = h.repository.accountFiles.CollectAccountObjects(t.Context(), h.db, time.Now().Add(25*time.Hour), 0, 100)
	require.NoError(t, err)
	current, err := h.service.MyAvatar(t.Context(), principal.ID)
	require.NoError(t, err)
	require.Equal(t, selected, current)
	for _, candidate := range candidates {
		candidateID := strings.TrimPrefix(candidate.URL, "/api/me/avatar-candidates/")
		require.Len(t, candidateID, 48)
		name := candidateID + ".png"
		readable, err := h.repository.accountFiles.AccountKeyReadable(t.Context(), h.db, principal.ID, "avatars/"+principal.ID.String()+"/"+name, time.Now().Add(25*time.Hour))
		require.NoError(t, err)
		require.Equal(t, candidate.URL == chosen.URL, readable)
	}
}

func TestAvatarAuditFailureKeepsPreviousReferenceAndCollectsUnattachedFile(t *testing.T) {
	h := newIdentityHarness(t, "avatarrollback_")
	h.seedTenant(t, 7, "active")
	principal, err := h.service.Create(t.Context(), 7, "avatar-user", "VerySecurePassword123!", "Avatar user", "")
	require.NoError(t, err)
	first, err := h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{Style: "monster"})
	require.NoError(t, err)
	h.auditErr = errors.New("audit failed")
	_, err = h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{Style: "geometric"})
	require.ErrorContains(t, err, "audit failed")
	current, err := h.service.MyAvatar(t.Context(), principal.ID)
	require.NoError(t, err)
	require.Equal(t, first, current)
	q := filequery.Bind(h.db, h.prefix)
	count, err := q.AccountReference.WithContext(t.Context()).Where(q.AccountReference.State.Eq("active")).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	_, err = h.repository.accountFiles.CollectAccountObjects(t.Context(), h.db, time.Now().Add(25*time.Hour), 0, 100)
	require.NoError(t, err)
	count, err = q.Object.WithContext(t.Context()).Where(q.Object.State.Eq("verified")).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestMyAvatarMissingReferenceDoesNotMutateOnGET(t *testing.T) {
	h := newIdentityHarness(t, "avatarreadonly_")
	h.seedTenant(t, 7, "active")
	principal, err := h.service.Create(t.Context(), 7, "avatar-user", "VerySecurePassword123!", "Avatar user", "")
	require.NoError(t, err)
	q := identityquery.BindWithNames(h.db, h.repository.names)
	_, err = q.Principals.WithContext(t.Context()).Where(q.Principals.ID.Eq(principal.ID)).Update(q.Principals.Avatar, "")
	require.NoError(t, err)
	_, err = h.service.MyAvatar(t.Context(), principal.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.Empty(t, h.principal(t, principal.ID).Avatar)
	files := filequery.Bind(h.db, h.prefix)
	count, err := files.Object.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, h.service.backfillMissingAvatars(t.Context()))
	avatar, err := h.service.MyAvatar(t.Context(), principal.ID)
	require.NoError(t, err)
	require.NotEmpty(t, avatar.URL)
	require.NoError(t, h.service.backfillMissingAvatars(t.Context()))
	count, err = files.Object.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "startup backfill is idempotent")
}
