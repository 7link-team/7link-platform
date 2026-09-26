package identity

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	identityquery "7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// avatarTestImage paints a frame around a centered square so a decoded result
// proves which region survived the square center crop.
func avatarTestImage(width, height int, frame, center color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(frame), image.Point{}, draw.Src)
	side := min(width, height)
	x, y := (width-side)/2, (height-side)/2
	draw.Draw(img, image.Rect(x, y, x+side, y+side), image.NewUniform(center), image.Point{}, draw.Src)
	return img
}

func avatarTestPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, img))
	return out.Bytes()
}

func avatarTestJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, jpeg.Encode(&out, img, nil))
	return out.Bytes()
}

func avatarDataURL(mime string, raw []byte) string {
	return "data:image/" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// legacyAvatarName is a storage name of the exact shape avatarStorageName
// accepts: 48 hexadecimal characters plus the .png suffix.
func legacyAvatarName(seed string) string {
	return strings.Repeat(seed, 48/len(seed)) + ".png"
}

func TestAvatarPixelsDrawsEveryStyleAndRejectsUnknownOnes(t *testing.T) {
	t.Parallel()

	for _, style := range avatarStyles {
		pixels, actual, err := avatarPixels(avatarInput{Style: style})
		require.NoError(t, err, style)
		require.Equal(t, style, actual, "a requested style must be the style that is drawn")
		config, format, err := image.DecodeConfig(bytes.NewReader(pixels))
		require.NoError(t, err, style)
		require.Equal(t, "png", format)
		require.Equal(t, 256, config.Width)
		require.Equal(t, 256, config.Height)
	}

	// An unspecified style is chosen by the server, never left blank and never
	// invented outside the published catalog.
	_, chosen, err := avatarPixels(avatarInput{})
	require.NoError(t, err)
	require.True(t, slices.Contains(avatarStyles, chosen), "generated style %q is outside the catalog", chosen)

	for _, style := range []string{"unicorn", "custom", "Robot", " pixel"} {
		_, _, err := avatarPixels(avatarInput{Style: style})
		require.ErrorIs(t, err, ErrInvalid, style)
	}
}

func TestAvatarPixelsNormalizesUploadsAndRejectsMalformedData(t *testing.T) {
	t.Parallel()

	frame, center := color.NRGBA{R: 255, A: 255}, color.NRGBA{G: 128, A: 255}
	wide := avatarTestPNG(t, avatarTestImage(400, 200, frame, center))
	tall := avatarTestJPEG(t, avatarTestImage(200, 400, frame, center))
	oversizedDimensions := avatarTestPNG(t, image.NewNRGBA(image.Rect(0, 0, 2049, 8)))
	oversizedBytes := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 2*1024*1024+1))

	for _, scenario := range []struct {
		name  string
		input avatarInput
	}{
		{"style must be custom", avatarInput{Style: "robot", ImageData: avatarDataURL("png", wide)}},
		{"style must not be blank", avatarInput{ImageData: avatarDataURL("png", wide)}},
		{"declared length is bounded", avatarInput{Style: "custom", ImageData: strings.Repeat("a", 2800001)}},
		{"a data URL needs a payload", avatarInput{Style: "custom", ImageData: "data:image/png;base64"}},
		{"only three media types are accepted", avatarInput{Style: "custom", ImageData: avatarDataURL("gif", wide)}},
		{"payload must be base64", avatarInput{Style: "custom", ImageData: "data:image/png;base64,!!not base64!!"}},
		{"decoded bytes are bounded", avatarInput{Style: "custom", ImageData: "data:image/png;base64," + oversizedBytes}},
		{"payload must be an image", avatarInput{Style: "custom", ImageData: avatarDataURL("png", []byte("this is not an image"))}},
		{"dimensions are bounded", avatarInput{Style: "custom", ImageData: avatarDataURL("png", oversizedDimensions)}},
		{"declared type must match the bytes", avatarInput{Style: "custom", ImageData: avatarDataURL("jpeg", wide)}},
		{"a webp header must match the bytes", avatarInput{Style: "custom", ImageData: avatarDataURL("webp", wide)}},
		{"truncated images are refused", avatarInput{Style: "custom", ImageData: avatarDataURL("png", wide[:40])}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			pixels, style, err := avatarPixels(scenario.input)
			require.ErrorIs(t, err, ErrInvalid)
			require.Nil(t, pixels)
			require.Empty(t, style)
		})
	}

	for _, accepted := range []struct {
		name string
		data string
	}{
		{"landscape png", avatarDataURL("png", wide)},
		{"portrait jpeg", avatarDataURL("jpeg", tall)},
	} {
		t.Run(accepted.name, func(t *testing.T) {
			pixels, style, err := avatarPixels(avatarInput{Style: "custom", ImageData: accepted.data})
			require.NoError(t, err)
			require.Equal(t, "custom", style)
			decoded, format, err := image.Decode(bytes.NewReader(pixels))
			require.NoError(t, err)
			require.Equal(t, "png", format, "every stored avatar is re-encoded as PNG")
			require.Equal(t, image.Rect(0, 0, 256, 256), decoded.Bounds())
			// The centered square filled the whole crop, so the frame that sat
			// outside it must not survive into the normalized avatar.
			for _, point := range []image.Point{{X: 4, Y: 4}, {X: 128, Y: 128}, {X: 251, Y: 251}} {
				r, g, b, _ := decoded.At(point.X, point.Y).RGBA()
				require.Less(t, r>>8, uint32(80), "the crop kept the frame instead of the subject at %v", point)
				require.Greater(t, g>>8, uint32(80), "the crop lost the subject at %v", point)
				require.Less(t, b>>8, uint32(80), "the crop kept the frame instead of the subject at %v", point)
			}
		})
	}
}

func TestMigrateAvatarStorageImportsLegacyKeysAndRefusesUnusableReferences(t *testing.T) {
	h := newIdentityHarness(t, "avatarstorage_")
	h.seedTenant(t, 7, "active")
	principal, err := h.service.Create(t.Context(), 7, "legacy-user", testPassword, "Legacy user", "")
	require.NoError(t, err)
	principals := identityquery.BindWithNames(h.db, h.repository.names).Principals
	setAvatar := func(t *testing.T, raw string) {
		t.Helper()
		_, err := principals.WithContext(t.Context()).Where(principals.ID.Eq(principal.ID)).Update(principals.Avatar, raw)
		require.NoError(t, err)
	}
	marshal := func(t *testing.T, avatar Avatar) string {
		t.Helper()
		raw, err := json.Marshal(avatar)
		require.NoError(t, err)
		return string(raw)
	}

	var imported []string
	recorder := files.LegacyImporter(func(_ context.Context, key string) error {
		imported = append(imported, key)
		return nil
	})

	// A repository without a writer has nothing to scan and must stay silent
	// rather than run a statement against an unbound table name.
	require.NoError(t, (&Service{repo: &Repository{}}).migrateAvatarStorage(t.Context(), recorder))
	require.Empty(t, imported)

	name := legacyAvatarName("ab")
	for _, prefix := range []string{"/api/avatars/", "/api/iam/avatars/"} {
		setAvatar(t, marshal(t, Avatar{Style: "custom", URL: prefix + principal.ID.String() + "/" + name}))
		imported = nil
		require.NoError(t, h.service.migrateAvatarStorage(t.Context(), recorder))
		require.Equal(t, []string{"avatars/" + principal.ID.String() + "/" + name}, imported,
			"the legacy key is derived from the principal, never from the stored URL prefix")
	}

	// An avatar already served from the governed route owns no legacy key.
	setAvatar(t, marshal(t, Avatar{Style: "pixel", URL: "/api/principals/" + principal.ID.String() + "/avatar"}))
	imported = nil
	require.NoError(t, h.service.migrateAvatarStorage(t.Context(), recorder))
	require.Empty(t, imported)

	// Unreadable and unrecognized references stop the migration instead of
	// silently dropping the file they point at.
	setAvatar(t, "{not json")
	require.ErrorContains(t, h.service.migrateAvatarStorage(t.Context(), recorder),
		"invalid avatar reference for principal "+principal.ID.String())
	setAvatar(t, marshal(t, Avatar{Style: "custom", URL: "/api/iam/avatars/" + principal.ID.String() + "/short.png"}))
	require.ErrorIs(t, h.service.migrateAvatarStorage(t.Context(), recorder), ErrInvalid)

	// An importer failure is reported against the principal it belongs to.
	setAvatar(t, marshal(t, Avatar{Style: "custom", URL: "/api/avatars/" + principal.ID.String() + "/" + name}))
	failure := errors.New("legacy storage unreachable")
	err = h.service.migrateAvatarStorage(t.Context(), func(context.Context, string) error { return failure })
	require.ErrorIs(t, err, failure)
	require.ErrorContains(t, err, "migrate avatar for principal "+principal.ID.String())
}

func TestMigrateAvatarStorageResumesAfterTheFirstBatch(t *testing.T) {
	h := newIdentityHarness(t, "avatarpaging_")
	current := func(id guid.ID) string {
		raw, err := json.Marshal(Avatar{Style: "pixel", URL: "/api/principals/" + id.String() + "/avatar"})
		require.NoError(t, err)
		return string(raw)
	}
	// One batch is 100 rows; 101 rows prove the scan resumes on the second page
	// instead of restarting it or stopping short.
	const rows = 101
	for i := range rows {
		id := guid.ID(500 + i)
		require.NoError(t, h.db.Exec(
			"INSERT INTO "+h.table("iam_principals")+
				" (id, login_name, email, display_name, avatar, status, email_verified, activation_required, created_at, updated_at)"+
				" VALUES (?, ?, '', ?, ?, 'active', 0, 0, ?, ?)",
			id, "paged-"+id.String(), "Paged "+id.String(), current(id), identityTestNow, identityTestNow,
		).Error)
	}
	seen := 0
	require.NoError(t, h.service.migrateAvatarStorage(t.Context(), func(context.Context, string) error {
		seen++
		return nil
	}))
	require.Zero(t, seen, "already governed avatars own no legacy key")

	// Making the last row of the second page legacy proves that page was read
	// rather than skipped by a cursor that never advanced.
	last := guid.ID(500 + rows - 1)
	raw, err := json.Marshal(Avatar{Style: "custom", URL: "/api/avatars/" + last.String() + "/" + legacyAvatarName("cd")})
	require.NoError(t, err)
	require.NoError(t, h.db.Exec(
		"UPDATE "+h.table("iam_principals")+" SET avatar = ? WHERE id = ?", string(raw), last).Error)
	var imported []string
	require.NoError(t, h.service.migrateAvatarStorage(t.Context(), func(_ context.Context, key string) error {
		imported = append(imported, key)
		return nil
	}))
	require.Equal(t, []string{"avatars/" + last.String() + "/" + legacyAvatarName("cd")}, imported)
}

// avatarPermissionChecker answers the cross-account avatar guard. It is kept
// apart from identityRouteAuthorizer so a denial or an outage on this one check
// can be told apart from the route's own authentication.
type avatarPermissionChecker struct {
	allowed bool
	err     error
}

func (c avatarPermissionChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return c.allowed, c.err
}

func (c avatarPermissionChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}

// avatarFieldChecker hides the profile fields named in hidden, so the avatar
// route can be exercised against a reader whose field policy withholds the
// identifying attributes the image belongs to.
type avatarFieldChecker struct {
	hidden []string
	err    error
}

func (c avatarFieldChecker) FieldConstraint(
	context.Context, guid.ID, guid.ID, string, guid.ID,
) (authz.FieldConstraint, error) {
	if c.err != nil {
		return authz.FieldConstraint{}, c.err
	}
	fields := map[string]authz.FieldDecision{}
	for _, field := range authz.ProfileFields() {
		decision := authz.DecideField(field, nil, nil)
		if slices.Contains(c.hidden, field.Name) {
			decision.Read = "omit"
		}
		fields[field.Name] = decision
	}
	return authz.FieldConstraint{Fields: fields}, nil
}

// avatarTokenVerifier refuses a request that carries no credential at all, so
// the image route's own rejection can be told apart from a missing image.
type avatarTokenVerifier struct{ principalID guid.ID }

func (v avatarTokenVerifier) Authenticate(_ context.Context, authorization, cookie string) (*authnext.Claims, error) {
	if authorization == "" && cookie == "" {
		return nil, authnext.ErrInvalidToken
	}
	return &authnext.Claims{
		PrincipalID: v.principalID, SubjectType: authnext.SubjectTypePrincipal,
		ACR: 1, AMR: []string{"password"},
	}, nil
}

type avatarHTTPHarness struct {
	*identityHarness
	api       humatest.TestAPI
	principal Principal
	other     Principal
}

func newAvatarHTTPHarness(t *testing.T) *avatarHTTPHarness {
	t.Helper()
	require.NoError(t, initializeIdentityLocales())
	h := newIdentityHarness(t, "avatarroute_")
	h.seedTenant(t, 7, "active")
	principal, err := h.service.Create(t.Context(), 7, "avatar-owner", testPassword, "Avatar owner", "")
	require.NoError(t, err)
	other, err := h.service.Create(t.Context(), 7, "avatar-other", testPassword, "Avatar other", "")
	require.NoError(t, err)
	h.service.fields = avatarFieldChecker{}
	h.service.avatarPermissions = avatarPermissionChecker{allowed: true}
	h.service.privacyBrowser.ValidateCSRF = func(_, origin, _ string) error {
		if origin != "https://account.example.test" {
			return errors.New("untrusted origin")
		}
		return nil
	}
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("avatar-test", "1.0.0"))
	RegisterHTTP(api, h.service, authz.NewRegistrar(
		authz.DefaultRegistry(),
		avatarTokenVerifier{principalID: principal.ID},
		identityRouteAuthorizer{userUpdateAllowed: true},
		identityMembershipChecker{},
	))
	return &avatarHTTPHarness{identityHarness: h, api: humatest.Wrap(t, api), principal: principal, other: other}
}

func TestAvatarHTTPPersonalRoutesRequireABrowserSessionAndATrustedOrigin(t *testing.T) {
	h := newAvatarHTTPHarness(t)
	cookie, origin := "Cookie: session=browser", "Origin: https://account.example.test"

	// A bearer token is not a browser session: the personal routes are cookie-only.
	require.Equal(t, http.StatusUnauthorized, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar").Code)
	require.Equal(t, http.StatusUnauthorized, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar", "Authorization: Bearer test", cookie).Code)
	require.Equal(t, http.StatusUnauthorized, h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", map[string]any{"style": "robot"}).Code)
	require.Equal(t, http.StatusUnauthorized, h.api.Post("/principals/"+h.principal.ID.String()+"/avatar-candidates", map[string]any{}).Code)

	// Reading before anything was stored reports absence, not an empty avatar.
	require.Equal(t, http.StatusNotFound, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar", cookie).Code)

	// A write without a trusted origin is refused before the avatar is touched.
	require.Equal(t, http.StatusForbidden, h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", cookie, map[string]any{"style": "robot"}).Code)
	require.Equal(t, http.StatusForbidden, h.api.Post("/principals/"+h.principal.ID.String()+"/avatar-candidates", cookie, map[string]any{}).Code)
	require.Equal(t, http.StatusNotFound, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar", cookie).Code)

	stored := h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", cookie, origin, map[string]any{"style": "monster"})
	require.Equal(t, http.StatusOK, stored.Code, stored.Body.String())
	written := decodeIdentityEnvelope[Avatar](t, stored).Data
	require.Equal(t, "monster", written.Style)
	require.Equal(t, "/api/principals/"+h.principal.ID.String()+"/avatar/content", written.URL)

	read := h.api.Get("/principals/"+h.principal.ID.String()+"/avatar", cookie)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	require.Equal(t, written, decodeIdentityEnvelope[Avatar](t, read).Data)

	// A rejected upload leaves the previously stored avatar in place.
	invalid := h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", cookie, origin, map[string]any{"style": "custom", "image_data": "not-a-data-url"})
	require.Equal(t, http.StatusUnprocessableEntity, invalid.Code, invalid.Body.String())
	require.Equal(t, written, decodeIdentityEnvelope[Avatar](t, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar", cookie)).Data)
}

func TestAvatarHTTPCandidatesStayPrivateToTheirOwner(t *testing.T) {
	h := newAvatarHTTPHarness(t)
	cookie, origin := "Cookie: session=browser", "Origin: https://account.example.test"

	generated := h.api.Post("/principals/"+h.principal.ID.String()+"/avatar-candidates", cookie, origin, map[string]any{"style": "geometric"})
	require.Equal(t, http.StatusOK, generated.Code, generated.Body.String())
	candidates := decodeIdentityEnvelope[[]Avatar](t, generated).Data
	require.Len(t, candidates, 6)
	chosen := candidates[3]
	require.Equal(t, "geometric", chosen.Style)
	candidateID := strings.TrimPrefix(chosen.URL, "/api/principals/"+h.principal.ID.String()+"/avatar-candidates/")
	require.Len(t, candidateID, 48)

	served := h.api.Get("/principals/"+h.principal.ID.String()+"/avatar-candidates/"+candidateID, cookie)
	require.Equal(t, http.StatusOK, served.Code, served.Body.String())
	require.Equal(t, "image/png", served.Header().Get("Content-Type"))
	require.Equal(t, "private, no-store", served.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", served.Header().Get("X-Content-Type-Options"))
	config, format, err := image.DecodeConfig(bytes.NewReader(served.Body.Bytes()))
	require.NoError(t, err)
	require.Equal(t, "png", format)
	require.Equal(t, 256, config.Width)

	// Candidates are private: no session means no image, and an unissued slot is
	// a miss rather than somebody else's candidate.
	require.Equal(t, http.StatusUnauthorized, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar-candidates/"+candidateID).Code)
	require.Equal(t, http.StatusNotFound, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar-candidates/"+strings.Repeat("f", 48), cookie).Code)

	selected := h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", cookie, origin, map[string]any{"candidate_url": chosen.URL, "style": chosen.Style})
	require.Equal(t, http.StatusOK, selected.Code, selected.Body.String())
	require.Equal(t, "geometric", decodeIdentityEnvelope[Avatar](t, selected).Data.Style)
	// Promoting a candidate consumes its private slot.
	require.Equal(t, http.StatusNotFound, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar-candidates/"+candidateID, cookie).Code)
}

func TestAvatarHTTPImageIsServedOnlyToItsOwnerOrAnAuthorizedReader(t *testing.T) {
	h := newAvatarHTTPHarness(t)
	cookie, origin, auth := "Cookie: session=browser", "Origin: https://account.example.test", "Authorization: Bearer test"
	own := "/principals/" + h.principal.ID.String() + "/avatar/content"
	foreign := "/principals/" + h.other.ID.String() + "/avatar/content"

	require.Equal(t, http.StatusUnauthorized, h.api.Get(own).Code)
	require.Equal(t, http.StatusNotFound, h.api.Get(own, auth).Code, "no avatar has been stored yet")

	require.Equal(t, http.StatusOK, h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", cookie, origin, map[string]any{"style": "robot"}).Code)
	served := h.api.Get(own, auth)
	require.Equal(t, http.StatusOK, served.Code, served.Body.String())
	require.Equal(t, "image/png", served.Header().Get("Content-Type"))
	require.Equal(t, "private, no-store", served.Header().Get("Cache-Control"))
	config, format, err := image.DecodeConfig(bytes.NewReader(served.Body.Bytes()))
	require.NoError(t, err)
	require.Equal(t, "png", format)
	require.Equal(t, 256, config.Width)

	// Another account's image needs a tenant selector and the tenant permission.
	require.Equal(t, http.StatusForbidden, h.api.Get(foreign, auth).Code)
	h.service.avatarPermissions = avatarPermissionChecker{allowed: false}
	require.Equal(t, http.StatusForbidden, h.api.Get(foreign+"?tenant_id=7", auth).Code)
	h.service.avatarPermissions = avatarPermissionChecker{err: ErrUnavailable}
	require.Equal(t, http.StatusServiceUnavailable, h.api.Get(foreign+"?tenant_id=7", auth).Code)
	h.service.avatarPermissions = nil
	require.Equal(t, http.StatusForbidden, h.api.Get(foreign+"?tenant_id=7", auth).Code)
	h.service.avatarPermissions = avatarPermissionChecker{allowed: true}

	// The permission alone is not enough: the reader also has to be allowed to
	// see who the image belongs to.
	h.service.fields = avatarFieldChecker{hidden: []string{"display_name"}}
	require.Equal(t, http.StatusForbidden, h.api.Get(foreign+"?tenant_id=7", auth).Code)
	h.service.fields = avatarFieldChecker{err: errors.New("field policy unreachable")}
	require.Equal(t, http.StatusServiceUnavailable, h.api.Get(foreign+"?tenant_id=7", auth).Code)
	h.service.fields = avatarFieldChecker{}

	// A principal outside the selected tenant is a miss, not an authorized read.
	require.Equal(t, http.StatusNotFound, h.api.Get("/principals/999999/avatar/content?tenant_id=7", auth).Code)
	require.Equal(t, http.StatusNotFound, h.api.Get(foreign+"?tenant_id=7", auth).Code,
		"the other account stored no avatar")

	_, err = h.service.SetMyAvatar(t.Context(), h.other.ID, avatarInput{Style: "pixel"})
	require.NoError(t, err)
	shared := h.api.Get(foreign+"?tenant_id=7", auth)
	require.Equal(t, http.StatusOK, shared.Code, shared.Body.String())
	require.NotEmpty(t, shared.Body.Bytes())

	// A suspended account's avatar stops being served.
	_, err = h.service.SetStatus(t.Context(), 7, h.other.ID, "disabled")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, h.api.Get(foreign+"?tenant_id=7", auth).Code)
}

func TestAvatarHTTPReportsMissingFileStorageAsAbsentImages(t *testing.T) {
	h := newAvatarHTTPHarness(t)
	cookie, origin, auth := "Cookie: session=browser", "Origin: https://account.example.test", "Authorization: Bearer test"
	own := "/principals/" + h.principal.ID.String() + "/avatar/content"
	require.Equal(t, http.StatusOK, h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", cookie, origin, map[string]any{"style": "pixel"}).Code)

	// A deployment without the files module reports the image as absent instead
	// of serving an empty body, and refuses writes rather than losing them.
	h.service.repo.resourceFiles = nil
	require.Equal(t, http.StatusNotFound, h.api.Get(own, auth).Code)
	require.Equal(t, http.StatusNotFound, h.api.Get("/principals/"+h.principal.ID.String()+"/avatar-candidates/"+strings.Repeat("a", 48), cookie).Code)
	require.Equal(t, http.StatusServiceUnavailable,
		h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", cookie, origin, map[string]any{"style": "robot"}).Code)

	// The reference survives without its bytes: the route still reports a miss.
	h.service.repo.resourceFiles = h.resources
	require.NoError(t, h.db.Exec("UPDATE "+h.table("file_resource_bindings")+" SET state = 'inactive', live_slot = NULL").Error)
	require.Equal(t, http.StatusNotFound, h.api.Get(own, auth).Code)
}

func TestMyAvatarReportsAbsenceWhenTheBindingIsGone(t *testing.T) {
	h := newIdentityHarness(t, "avatarmiss_")
	h.seedTenant(t, 7, "active")
	principal, err := h.service.Create(t.Context(), 7, "avatar-user", testPassword, "Avatar user", "")
	require.NoError(t, err)
	_, err = h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{Style: "robot"})
	require.NoError(t, err)

	require.NoError(t, h.db.Exec("UPDATE "+h.table("file_resource_bindings")+" SET state = 'inactive', live_slot = NULL").Error)
	_, err = h.service.MyAvatar(t.Context(), principal.ID)
	require.ErrorIs(t, err, ErrNotFound, "a reference without a readable file is not an avatar")
	require.NotEmpty(t, h.principal(t, principal.ID).Avatar, "reading must not erase the stored reference")

	// A suspended account exposes nothing at all, stored reference or not.
	_, err = h.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.NoError(t, err)
	_, err = h.service.MyAvatar(t.Context(), principal.ID)
	require.ErrorIs(t, err, ErrPrincipalInactive)
	_, err = h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{Style: "pixel"})
	require.ErrorIs(t, err, ErrPrincipalInactive)
	_, err = h.service.AvatarCandidates(t.Context(), principal.ID, "pixel")
	require.ErrorIs(t, err, ErrPrincipalInactive)
	_, err = h.service.AvatarCandidates(t.Context(), 424242, "pixel")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSetMyAvatarValidatesCandidateSelection(t *testing.T) {
	h := newIdentityHarness(t, "avatarselect_")
	h.seedTenant(t, 7, "active")
	principal, err := h.service.Create(t.Context(), 7, "avatar-user", testPassword, "Avatar user", "")
	require.NoError(t, err)
	valid := "/api/principals/" + principal.ID.String() + "/avatar-candidates/" + strings.Repeat("ab", 24)

	for _, scenario := range []struct {
		name  string
		input avatarInput
	}{
		{"unknown route", avatarInput{CandidateURL: "/api/avatars/" + strings.Repeat("ab", 24), Style: "robot"}},
		{"wrong token length", avatarInput{CandidateURL: "/api/principals/" + principal.ID.String() + "/avatar-candidates/" + strings.Repeat("ab", 20), Style: "robot"}},
		{"token must be hexadecimal", avatarInput{CandidateURL: "/api/principals/" + principal.ID.String() + "/avatar-candidates/" + strings.Repeat("zz", 24), Style: "robot"}},
		{"a selection carries no upload", avatarInput{CandidateURL: valid, Style: "robot", ImageData: "data:image/png;base64,AAAA"}},
		{"style must be a catalog style", avatarInput{CandidateURL: valid, Style: "custom"}},
		{"style is required", avatarInput{CandidateURL: valid}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := h.service.SetMyAvatar(t.Context(), principal.ID, scenario.input)
			require.ErrorIs(t, err, ErrInvalid)
		})
	}

	// A well-formed token that was never issued is a miss, not a silent write.
	_, err = h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{CandidateURL: valid, Style: "robot"})
	require.ErrorIs(t, err, ErrNotFound)
	require.Empty(t, h.principal(t, principal.ID).Avatar)

	// Avatars are unavailable altogether when the files module is absent.
	h.service.repo.resourceFiles = nil
	_, err = h.service.SetMyAvatar(t.Context(), principal.ID, avatarInput{Style: "robot"})
	require.ErrorIs(t, err, ErrUnavailable)
	_, _, _, err = h.repository.prepareAvatarFile(t.Context(), principal.ID, []byte("x"), "robot")
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestMigrateUngovernedAvatarsRefusesUnusableReferences(t *testing.T) {
	h := newIdentityHarness(t, "avatarungoverned_")
	h.seedTenant(t, 7, "active")
	principal, err := h.service.Create(t.Context(), 7, "avatar-user", testPassword, "Avatar user", "")
	require.NoError(t, err)
	principals := identityquery.BindWithNames(h.db, h.repository.names).Principals
	setAvatar := func(t *testing.T, avatar Avatar) {
		t.Helper()
		raw, err := json.Marshal(avatar)
		require.NoError(t, err)
		_, err = principals.WithContext(t.Context()).
			Where(principals.ID.Eq(principal.ID)).Update(principals.Avatar, string(raw))
		require.NoError(t, err)
	}

	setAvatar(t, Avatar{Style: "custom", URL: "/api/iam/avatars/" + principal.ID.String() + "/nope.png"})
	require.ErrorContains(t, h.service.migrateUngovernedAvatars(t.Context()),
		"invalid unbound avatar reference for principal "+principal.ID.String())

	// A legacy key whose bytes were already lost cannot be adopted.
	setAvatar(t, Avatar{Style: "custom", URL: "/api/iam/avatars/" + principal.ID.String() + "/" + legacyAvatarName("ef")})
	require.ErrorContains(t, h.service.migrateUngovernedAvatars(t.Context()),
		"adopt avatar for principal "+principal.ID.String())

	// Without the files module the adoption pass reports unavailability rather
	// than leaving half of the references migrated.
	h.service.repo.resourceFiles = nil
	require.ErrorIs(t, h.service.migrateUngovernedAvatars(t.Context()), ErrUnavailable)
	require.ErrorIs(t, (&Service{repo: &Repository{}}).migrateUngovernedAvatars(t.Context()), ErrUnavailable)
}

func TestBackfillMissingAvatarsSkipsAccountsItMustNotTouch(t *testing.T) {
	h := newIdentityHarness(t, "avatarbackfill_")
	h.seedTenant(t, 7, "active")
	active, err := h.service.Create(t.Context(), 7, "active-user", testPassword, "Active user", "")
	require.NoError(t, err)
	disabled, err := h.service.Create(t.Context(), 7, "disabled-user", testPassword, "Disabled user", "")
	require.NoError(t, err)
	_, err = h.service.SetStatus(t.Context(), 7, disabled.ID, "disabled")
	require.NoError(t, err)

	require.NoError(t, h.service.backfillMissingAvatars(t.Context()))
	require.NotEmpty(t, h.principal(t, active.ID).Avatar)
	require.Empty(t, h.principal(t, disabled.ID).Avatar, "a suspended account is not given an avatar")

	// A repository without a writer has nothing to backfill.
	require.NoError(t, (&Service{repo: &Repository{}}).backfillMissingAvatars(t.Context()))
}

// TestAvatarHTTPRefusesBearerCallersOnThePersonalRoutes proves the personal
// avatar routes insist on a browser session of their own: a deployment whose
// token verifier accepts a bearer credential must still not let it read or
// replace somebody's avatar through /me.
func TestAvatarHTTPRefusesBearerCallersOnThePersonalRoutes(t *testing.T) {
	h := newIdentityHTTPHarness(t, true)

	for name, response := range map[string]*httptest.ResponseRecorder{
		"read":             h.api.Get("/principals/" + "11" + "/avatar"),
		"replace":          h.api.Put("/principals/"+"11"+"/avatar", map[string]any{"style": "robot"}),
		"candidates":       h.api.Post("/principals/"+"11"+"/avatar-candidates", map[string]any{}),
		"candidate image":  h.api.Get("/principals/" + "11" + "/avatar-candidates/" + strings.Repeat("a", 48)),
		"bearer candidate": h.api.Get("/principals/"+"11"+"/avatar-candidates/"+strings.Repeat("a", 48), "Authorization: Bearer test"),
	} {
		require.Equal(t, http.StatusUnauthorized, response.Code, "%s: %s", name, response.Body.String())
	}
}

func TestAvatarHTTPRejectsUnusableIdentifiersAndUnreachableStorage(t *testing.T) {
	h := newAvatarHTTPHarness(t)
	cookie, origin, auth := "Cookie: session=browser", "Origin: https://account.example.test", "Authorization: Bearer test"
	require.Equal(t, http.StatusOK, h.api.Put("/principals/"+h.principal.ID.String()+"/avatar", cookie, origin, map[string]any{"style": "robot"}).Code)

	// A numeric identifier outside the platform's range is refused rather than
	// silently truncated into somebody else's account.
	overflow := h.api.Get("/principals/9999999999999999999/avatar/content", auth)
	require.Equal(t, http.StatusUnprocessableEntity, overflow.Code, overflow.Body.String())

	_, err := h.service.AvatarCandidates(t.Context(), h.other.ID, "unicorn")
	require.ErrorIs(t, err, ErrInvalid, "an unknown style is refused before any file is staged")

	// A suspended account generates nothing: candidates are private material
	// that a closed account must stop producing.
	_, err = h.service.SetStatus(t.Context(), 7, h.principal.ID, "disabled")
	require.NoError(t, err)
	refused := h.api.Post("/principals/"+h.principal.ID.String()+"/avatar-candidates", cookie, origin, map[string]any{"style": "robot"})
	require.Equal(t, http.StatusConflict, refused.Code, refused.Body.String())

	// The identities table is unreachable: the image route reports the outage
	// instead of answering that the account has no avatar.
	require.NoError(t, h.db.Exec("PRAGMA foreign_keys = OFF").Error)
	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_principals")).Error)
	broken := h.api.Get("/principals/"+h.principal.ID.String()+"/avatar/content", auth)
	require.Equal(t, http.StatusInternalServerError, broken.Code, broken.Body.String())
}
