package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type profilePermissions struct{ avatarPermissionChecker }

func TestHumanContactsExcludeForeignInactiveAndServiceAccounts(t *testing.T) {
	h := newServiceAccountHarness(t, "contacts_")
	ctx := t.Context()
	people := make([]Principal, 0, 4)
	for _, name := range []string{"Reader", "Alice", "Alicia", "Inactive"} {
		p, err := h.service.Create(ctx, 7, strings.ToLower(name), testPassword, name, strings.ToLower(name)+"@example.test")
		require.NoError(t, err)
		people = append(people, p)
	}
	_, err := h.service.Create(ctx, 8, "foreign", testPassword, "Alice foreign", "foreign@example.test")
	require.NoError(t, err)
	_, err = h.service.CreateServiceAccount(ctx, 7, "robot", "Alice robot", "", nil)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("principal_id = ?", people[3].ID).Update("status", "disabled").Error)
	first, err := h.service.humanContacts(ctx, 7, people[0].ID, humanContactsInput{Search: "ali", Limit: 1})
	require.NoError(t, err)
	require.True(t, first.HasMore)
	require.Equal(t, people[1].ID, first.Items[0].ID)
	second, err := h.service.humanContacts(ctx, 7, people[0].ID, humanContactsInput{Search: "ali", After: first.NextAfter, Limit: 1})
	require.NoError(t, err)
	require.False(t, second.HasMore)
	require.Equal(t, people[2].ID, second.Items[0].ID)
	require.NoError(t, h.db.Table(h.table("iam_principals")).Where("id = ?", people[2].ID).Update("status", "disabled").Error)
	page, err := h.service.humanContacts(ctx, 7, people[0].ID, humanContactsInput{Limit: 30})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.NoError(t, initializeIdentityLocales())
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("contacts", "1"))
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), identityTokenVerifier{principalID: people[0].ID}, identityRouteAuthorizer{}, identityMembershipChecker{})
	registerProfileHTTP(api, h.service, registrar)
	httpAPI := humatest.Wrap(t, api)
	response := httpAPI.Get("/tenants/7/human-contacts?search=ali", "Authorization: Bearer fixture")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"display_name":"Alice"`)
	require.NotContains(t, response.Body.String(), "example.test")
	require.NotContains(t, response.Body.String(), "login_name")
	filtered := httpAPI.Get("/tenants/7/human-contacts?search=no-matching-human", "Authorization: Bearer fixture")
	require.Equal(t, http.StatusOK, filtered.Code, filtered.Body.String())
	var filteredPage struct {
		Data HumanContactsPage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(filtered.Body.Bytes(), &filteredPage))
	require.Empty(t, filteredPage.Data.Items, "the public search parameter must actually filter contacts")
	require.Equal(t, http.StatusForbidden, httpAPI.Get("/tenants/8/human-contacts", "Authorization: Bearer fixture").Code)
	failure := errors.New("contact projection failed")
	require.NoError(t, h.db.Callback().Row().Before("gorm:row").Register("fixture:contacts", func(tx *gorm.DB) { tx.AddError(failure) }))
	page, err = h.service.humanContacts(ctx, 7, people[0].ID, humanContactsInput{Limit: 30})
	require.ErrorIs(t, err, failure)
	require.Empty(t, page.Items, "projection failure must return no partial contacts")
	require.NoError(t, h.db.Callback().Row().Remove("fixture:contacts"))
	for _, in := range []humanContactsInput{{Limit: 0}, {Limit: 101}, {Limit: 1, After: -1}, {Limit: 1, Search: strings.Repeat("x", 513)}} {
		_, err = h.service.humanContacts(ctx, 7, people[0].ID, in)
		require.ErrorIs(t, err, ErrInvalid)
	}
	_, err = (*Service)(nil).humanContacts(ctx, 7, people[0].ID, humanContactsInput{Limit: 1})
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = h.service.humanContacts(ctx, 8, people[0].ID, humanContactsInput{Limit: 1})
	require.ErrorIs(t, err, ErrProfileDenied)
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("principal_id = ?", people[0].ID).Update("status", "disabled").Error)
	_, err = h.service.humanContacts(ctx, 7, people[0].ID, humanContactsInput{Limit: 1})
	require.ErrorIs(t, err, ErrProfileDenied)
	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_tenant_members")).Error)
	page, err = h.service.humanContacts(ctx, 7, people[0].ID, humanContactsInput{Limit: 1})
	require.Error(t, err)
	require.Empty(t, page.Items)
}

func (p profilePermissions) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return p.allowed, p.err
}

func TestSelfProfileDomainCannotRebindActorAndRejectsOversizeContact(t *testing.T) {
	h, p := closureHarness(t)
	name := "My nickname"
	_, err := h.service.UpdateGlobal(t.Context(), p.ID, &name, nil, nil)
	require.ErrorIs(t, err, ErrProfileDenied)
	wrongActor := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: p.ID + 1})
	h.service.avatarPermissions = nil
	_, err = h.service.UpdateGlobal(wrongActor, p.ID, &name, nil, nil)
	require.ErrorIs(t, err, ErrUnavailable)
	h.service.avatarPermissions = avatarPermissionChecker{allowed: false}
	_, err = h.service.UpdateGlobal(wrongActor, p.ID, &name, nil, nil)
	require.ErrorIs(t, err, ErrProfileDenied)
	permissionFailure := errors.New("current permission unavailable")
	h.service.avatarPermissions = profilePermissions{avatarPermissionChecker{err: permissionFailure}}
	_, err = h.service.UpdateGlobal(wrongActor, p.ID, &name, nil, nil)
	require.ErrorIs(t, err, permissionFailure)
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: p.ID})
	oversized := strings.Repeat("x", 321)
	_, err = h.service.UpdateGlobal(ctx, p.ID, nil, &oversized, nil)
	require.ErrorIs(t, err, ErrInvalid)
	require.Equal(t, p.Email, h.principal(t, p.ID).Email)
	require.Equal(t, p.DisplayName, h.principal(t, p.ID).DisplayName)
}

func TestSelfProfileUsesSessionSelfBoundaryAndCanonicalRevocation(t *testing.T) {
	h, p := closureHarness(t)
	require.NoError(t, initializeIdentityLocales())
	h.service.privacyBrowser.ValidateCSRF = func(method, origin, cookie string) error {
		if method != http.MethodPatch || origin != "https://account.example.test" || cookie != "browser" {
			return errors.New("CSRF")
		}
		return nil
	}
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("profile-test", "1"))
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), identityTokenVerifier{principalID: p.ID}, identityRouteAuthorizer{}, identityMembershipChecker{})
	registerProfileHTTP(api, h.service, registrar)
	httpAPI := humatest.Wrap(t, api)
	path := "/principals/" + p.ID.String() + "/profile"
	cookie, origin := "Cookie: browser", "Origin: https://account.example.test"
	require.Equal(t, http.StatusUnauthorized, httpAPI.Get(path).Code)
	require.Equal(t, http.StatusUnauthorized, httpAPI.Patch(path, cookie, "Authorization: Bearer test", map[string]any{"display_name": "Changed"}).Code)
	require.Equal(t, http.StatusForbidden, httpAPI.Patch(path, cookie, map[string]any{"display_name": "Changed"}).Code)
	require.Equal(t, http.StatusNotFound, httpAPI.Get("/principals/999/profile", cookie).Code)
	for _, body := range []map[string]any{{}, {"display_name": " "}, {"phone": "not a number"}, {"principal_id": "999", "display_name": "Spoofed"}} {
		require.Equal(t, http.StatusUnprocessableEntity, httpAPI.Patch(path, cookie, origin, body).Code)
	}
	// Personal profile access survives leaving the last tenant; no user_update
	// or tenant membership grant is introduced by this endpoint.
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("principal_id = ?", p.ID).Delete(map[string]any{}).Error)
	h.seedSession(t, 1, p.ID)
	updated := httpAPI.Patch(path, cookie, origin, map[string]any{"display_name": "My nickname", "phone": "+1 (202) 555-0123"})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.Contains(t, updated.Body.String(), `"display_name":"My nickname"`)
	require.Equal(t, "+12025550123", h.principal(t, p.ID).Phone)
	var alive int64
	require.NoError(t, h.db.Table(h.table("iam_sessions")).Where("principal_id = ? AND revoked_at IS NULL", p.ID).Count(&alive).Error)
	require.EqualValues(t, 1, alive, "nickname/unverified contact change must preserve sessions")
	changed := httpAPI.Patch(path, cookie, origin, map[string]any{"email": "New@Example.test"})
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	require.Contains(t, changed.Body.String(), `"email":"new@example.test"`)
	require.Contains(t, changed.Body.String(), `"email_verified":false`)
	require.NoError(t, h.db.Table(h.table("iam_sessions")).Where("principal_id = ? AND revoked_at IS NULL", p.ID).Count(&alive).Error)
	require.Zero(t, alive, "canonical email change must revoke existing sessions")
	read := httpAPI.Get(path, cookie)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	contract := httpAPI.Do(http.MethodOptions, path+"?method=PATCH", cookie)
	require.Equal(t, http.StatusOK, contract.Code, contract.Body.String())
	require.Contains(t, contract.Body.String(), `"operation_id":"identity-update-my-profile"`)
	require.Equal(t, http.StatusUnauthorized, httpAPI.Do(http.MethodOptions, path+"?method=PATCH").Code)
	require.NotContains(t, read.Body.String(), "credential_version")
	require.NotContains(t, read.Body.String(), "password")
	h.seedSession(t, 2, p.ID)
	before := h.principal(t, p.ID)
	h.service.repo.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	failed := httpAPI.Patch(path, cookie, origin, map[string]any{"email": "rollback@example.test"})
	require.Equal(t, http.StatusInternalServerError, failed.Code)
	require.NotContains(t, failed.Body.String(), "audit unavailable")
	require.Equal(t, before.Email, h.principal(t, p.ID).Email)
	require.NoError(t, h.db.Table(h.table("iam_sessions")).Where("principal_id = ? AND revoked_at IS NULL", p.ID).Count(&alive).Error)
	require.EqualValues(t, 1, alive, "failed audit rolls back profile and credential/session revocation together")
	for _, op := range api.OpenAPI().Paths["/principals/{principal_id}/profile"].Get.Security {
		require.Contains(t, op, authz.SessionScheme)
	}
	// Database failure must produce an error, not an empty or fabricated profile.
	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_principals")).Error)
	require.Equal(t, http.StatusInternalServerError, httpAPI.Get(path, cookie).Code)
	h.service.repo.runtime = nil
	h.service.repo.db = nil
	require.Equal(t, http.StatusInternalServerError, httpAPI.Get(path, cookie).Code, "canonical repository failure is concealed by identityError")
}
