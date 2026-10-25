package clientwork

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProjectClientAccessReportsCurrentSignerWithoutGrantingOnFailure(t *testing.T) {
	m, _, _, _ := harness(t)
	for _, stage := range []string{"can_accept", "not_signer", "delivery_forbidden", "store_failure"} {
		t.Run(stage, func(t *testing.T) {
			m.clientSigners.ContactOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) {
				switch stage {
				case "not_signer":
					return 0, ErrForbidden
				case "delivery_forbidden":
					return 0, delivery.ErrWorkForbidden
				case "store_failure":
					return 0, errors.New("private signer store failure")
				}
				return 4, nil
			}
			out, err := m.projectAccessHTTP(testContext(t, 4), &ListInput{EntityID: "3"})
			if stage == "store_failure" {
				require.Error(t, err)
				require.Nil(t, out)
				return
			}
			require.NoError(t, err)
			require.True(t, out.Body.Data.External)
			require.Equal(t, stage == "can_accept", out.Body.Data.CanAccept)
		})
	}
	out, err := m.accessHTTP(testContext(t, 4), &ClientAccessInput{Limit: 0})
	require.NoError(t, err)
	require.Len(t, out.Body.Data.Projects.Items, 1)
}

type testVerifier struct{ actor guid.ID }

func TestExternalBoundaryResolvesEntityBeforeTakingTheOnlyConnection(t *testing.T) {
	m, db, _, _ := harness(t)
	m.verifier = testVerifier{4}
	raw, err := db.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	m.owner = func(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
		var version int
		err := db.WithContext(ctx).Table("settings_probe").Select("version").Scan(&version).Error
		return authz.ResourceOwner{TenantID: 1, EntityID: id}, version == 1, err
	}
	called := false
	h := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	out := httptest.NewRecorder()
	h.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/entities/3/client-access", nil).WithContext(ctx))
	require.Equal(t, http.StatusNoContent, out.Code, out.Body.String())
	require.True(t, called)
	require.Equal(t, "private, no-store", out.Header().Get("Cache-Control"))
}

type delegatedTestVerifier struct{}

func (delegatedTestVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: 4, ClientID: "41", OAuthGrantID: "grant"}, nil
}

func TestExternalBoundaryDefersDelegatedTokensToRouteGuards(t *testing.T) {
	m, _, _, _ := harness(t)
	m.verifier = delegatedTestVerifier{}
	m.owner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		t.Fatal("resource lookup must follow token-origin guard")
		return authz.ResourceOwner{}, false, nil
	}
	for _, path := range []string{"/channels/999/messages", "/entities/999/work-items", "/tenants/999/notifications", "/oauth/userinfo", "/sessions"} {
		t.Run(path, func(t *testing.T) {
			called := false
			h := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusUnauthorized)
			}))
			out := httptest.NewRecorder()
			h.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
			require.True(t, called)
			require.Equal(t, http.StatusUnauthorized, out.Code)
		})
	}
}

func (v testVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: v.actor}, nil
}
func TestExternalBoundaryDeniesInternalAndFutureRoutes(t *testing.T) {
	m, _, _, clients := harness(t)
	m.verifier = testVerifier{4}
	m.owner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 1, EntityID: 3}, true, nil
	}
	h := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, path := range []string{"/entities/3/work-items", "/entities/3/project", "/entities/3/test-cases", "/entities/3/project/settings", "/tenants/1/events/stream", "/tenants/1/search", "/tenants/1/notifications", "/spaces/8/files/9/content", "/entities/3/requirement-sheets/4/future", "/tenants/1/principals/5/entities"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, 404, r.Code)
			require.NotContains(t, r.Body.String(), "internal")
		})
	}
	for _, path := range []string{"/entities/3/client-access", "/tenants/1/client-access", "/entities/3/requirement-sheets", "/entities/3/delivery-packages/4/artifacts/40/content", "/entities/3/artifact-previews/4/content", "/channels/7/messages"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, 204, r.Code, path)
		require.Equal(t, "private, no-store", r.Header().Get("Cache-Control"))
	}
	clients.revoked = true
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/channels/7/messages", nil))
	require.Equal(t, 404, r.Code)
	m.verifier = testVerifier{2}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/entities/3/work-items", nil))
	require.Equal(t, 204, r.Code)
}
func TestExternalWhitelistCommandsAreClosedByDefault(t *testing.T) {
	for _, in := range []struct {
		method, path string
		allowed      bool
	}{{"POST", "entities/3/requirement-sheets", false}, {"POST", "entities/3/requirement-sheets/4/signoffs", true}, {"POST", "entities/3/requirement-sheets/4/publish", false}, {"POST", "entities/3/client-changes", true}, {"POST", "entities/3/delivery-packages/4/receipts", true}, {"POST", "entities/3/delivery-packages/4/publish", false}, {"PATCH", "entities/3/project/settings", false}, {"GET", "channels/7/messages/8/attachments/9/content", true}, {"PUT", "channels/7/members/4", false}} {
		require.Equal(t, in.allowed, externalRouteAllowed(in.method, strings.Split(in.path, "/"), 4), in.path)
	}
}
func TestClientDiscoveryRevocationAndOwnPrincipal(t *testing.T) {
	m, _, _, clients := harness(t)
	out, e := m.accessHTTP(testContext(t, 4), &ClientAccessInput{Limit: 20})
	require.NoError(t, e)
	require.True(t, out.Body.Data.External)
	require.Equal(t, guid.ID(4), out.Body.Data.PrincipalID)
	require.Len(t, out.Body.Data.Projects.Items, 1)
	clients.revoked = true
	out, e = m.accessHTTP(testContext(t, 4), &ClientAccessInput{Limit: 20})
	require.NoError(t, e)
	require.True(t, out.Body.Data.External)
	require.Empty(t, out.Body.Data.Projects.Items)
	_, e = m.projectAccessHTTP(testContext(t, 4), &ListInput{EntityID: "3"})
	require.Error(t, e)
	out, e = m.accessHTTP(testContext(t, 2), &ClientAccessInput{Limit: 20})
	require.NoError(t, e)
	require.False(t, out.Body.Data.External)
}

func TestExternalBoundaryLookupFailuresNeverReachHandlers(t *testing.T) {
	m, _, _, clients := harness(t)
	m.verifier = testVerifier{4}
	m.owner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	}
	h := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	check := func(path string, status int) {
		t.Helper()
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, status, r.Code, path)
	}
	for _, path := range []string{"/entities/bad/work-items", "/entities/999/work-items", "/channels/bad/messages", "/tenants/bad/client-access"} {
		check(path, 404)
	}
	m.owner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, ErrUnavailable
	}
	check("/entities/999/work-items", 503)
	clients.channelErr = ErrUnavailable
	check("/channels/7/messages", 503)
	clients.channelErr = nil
	clients.externalErr = ErrUnavailable
	check("/tenants/1/client-access", 503)
	clients.externalErr = nil
	for _, path := range []string{"/health", "/openapi.json", "/docs", "/me", "/sessions", "/auth/login", "/principals/4/profile"} {
		check(path, 204)
	}
	m.verifier = nil
	check("/entities/3/requirement-sheets", 503)
}

func TestExternalProjectionTransportMatrixCannotBypassBoundaryOrCacheDenials(t *testing.T) {
	m, _, _, _ := harness(t)
	m.verifier = testVerifier{4}
	m.owner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 1, EntityID: 3}, true, nil
	}
	for _, path := range []string{
		"/entities/3/project", "/entities/3/work-items/44", "/entities/3/executions/45",
		"/spaces/8/files/9/content", "/spaces/8/files/9/thumbnail", "/files/9/content",
		"/entities/3/artifact-previews/9/thumbnail", "/entities/3/delivery-packages/9/export",
		"/entities/3/client-issues/9/internal", "/entities/3/artifact-comments/9/attachments",
		"/tenants/1/search?q=private", "/entities/3/search?q=private", "/tenants/1/events/stream",
		"/entities/3/events/stream", "/tenants/1/export", "/entities/3/work-items/count",
		"/entities/3/delivery-packages/9/artifacts/40/thumbnail", "/entities/3/artifact-previews/count",
	} {
		t.Run(path, func(t *testing.T) {
			// The actual module boundary must terminate before a source handler,
			// including conditional/range requests and event-stream negotiation.
			called := false
			h := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				_, _ = w.Write([]byte("PRIVATE_SOURCE_MARKER"))
			}))
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("If-None-Match", `"old-private-cache"`)
			r.Header.Set("Range", "bytes=0-99")
			r.Header.Set("Accept", "text/event-stream")
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			require.False(t, called)
			require.Equal(t, http.StatusNotFound, out.Code)
			require.NotContains(t, out.Body.String(), "PRIVATE_SOURCE_MARKER")
			require.Equal(t, "private, no-store", out.Header().Get("Cache-Control"))
			require.Contains(t, out.Header().Values("Vary"), "Authorization")
			require.Contains(t, out.Header().Values("Vary"), "Cookie")
		})
	}
}

func TestClientDiscoveryAndProjectAccessFailClosedWithMissingInfrastructure(t *testing.T) {
	m, _, _, clients := harness(t)
	ctx := testContext(t, 4)
	clients.projectIDs = []guid.ID{3, 6}
	first, e := m.accessHTTP(ctx, &ClientAccessInput{Limit: 1})
	require.NoError(t, e)
	require.False(t, first.Body.Data.Projects.HasMore, "an inaccessible project must not create a phantom page")
	require.Empty(t, first.Body.Data.Projects.NextCursor)
	_, e = m.accessHTTP(ctx, &ClientAccessInput{Limit: 1, Cursor: "bad!"})
	require.Error(t, e)
	clients.projectErr = ErrUnavailable
	_, e = m.accessHTTP(ctx, &ClientAccessInput{Limit: 1})
	require.Error(t, e)
	clients.projectErr = nil
	clients.clientErr = ErrUnavailable
	_, e = m.accessHTTP(ctx, &ClientAccessInput{Limit: 1})
	require.Error(t, e)
	clients.clientErr = nil
	_, e = m.projectAccessHTTP(ctx, &ListInput{EntityID: "bad"})
	require.Error(t, e)
	_, e = m.accessHTTP(testContext(t, 9), &ClientAccessInput{Limit: 1})
	require.Error(t, e)
	m.runtime = nil
	_, e = m.accessHTTP(ctx, &ClientAccessInput{})
	require.Error(t, e)
	_, e = m.projectAccessHTTP(ctx, &ListInput{EntityID: "3"})
	require.Error(t, e)
}

type discoveryProjects struct {
	delivery.ClientWorkAccess
	missing, failure bool
}

func (p discoveryProjects) ClientSummaryOn(_ context.Context, _ *gorm.DB, _ guid.ID, id guid.ID) (delivery.ClientProjectSummary, error) {
	if p.failure {
		return delivery.ClientProjectSummary{}, ErrUnavailable
	}
	if p.missing {
		return delivery.ClientProjectSummary{}, gorm.ErrRecordNotFound
	}
	return delivery.ClientProjectSummary{ID: id, Name: "Visible project"}, nil
}

type discoveryClients struct {
	collab.ClientProjectAccess
	unordered bool
}

func (c discoveryClients) IsClientOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
func (c discoveryClients) ProjectsOn(_ context.Context, _ *gorm.DB, _, _, after guid.ID, _ int) ([]guid.ID, error) {
	if c.unordered {
		return []guid.ID{after}, nil
	}
	ids := []guid.ID{}
	for _, id := range []guid.ID{3, 6} {
		if id > after {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func TestClientDiscoveryPaginatesOnlyAvailableCurrentProjectSummaries(t *testing.T) {
	m, _, projects, clients := harness(t)
	m.projects = discoveryProjects{ClientWorkAccess: projects}
	m.clients = discoveryClients{ClientProjectAccess: clients}
	ctx := testContext(t, 4)
	first, err := m.accessHTTP(ctx, &ClientAccessInput{Limit: 1})
	require.NoError(t, err)
	require.True(t, first.Body.Data.Projects.HasMore)
	require.Equal(t, guid.ID(3), first.Body.Data.Projects.Items[0].ID)
	require.NotEmpty(t, first.Body.Data.Projects.NextCursor)
	second, err := m.accessHTTP(ctx, &ClientAccessInput{Limit: 1, Cursor: first.Body.Data.Projects.NextCursor})
	require.NoError(t, err)
	require.False(t, second.Body.Data.Projects.HasMore)
	require.Equal(t, guid.ID(6), second.Body.Data.Projects.Items[0].ID)
	m.projects = discoveryProjects{ClientWorkAccess: projects, missing: true}
	hidden, err := m.accessHTTP(ctx, &ClientAccessInput{Limit: 1})
	require.NoError(t, err)
	require.Empty(t, hidden.Body.Data.Projects.Items)
	require.False(t, hidden.Body.Data.Projects.HasMore)
	m.projects = discoveryProjects{ClientWorkAccess: projects, failure: true}
	out, err := m.accessHTTP(ctx, &ClientAccessInput{Limit: 1})
	require.Error(t, err)
	require.Nil(t, out)
	m.projects = discoveryProjects{ClientWorkAccess: projects}
	m.clients = discoveryClients{ClientProjectAccess: clients, unordered: true}
	out, err = m.accessHTTP(ctx, &ClientAccessInput{Limit: 1})
	require.Error(t, err)
	require.Nil(t, out)
}

func TestExternalBoundaryAllowsOnlyExactPublicCommentAndSelfPermissionRoutes(t *testing.T) {
	m, _, _, _ := harness(t)
	m.verifier = testVerifier{4}
	m.owner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 1, EntityID: 3}, true, nil
	}
	item := strings.Repeat("a", 64)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/entities/3/delivery-packages/7/rubric-scores", 204},
		{"GET", "/tenants/1/principals/4/permissions", 204},
		{"GET", "/tenants/1/principals/4/menus", 204},
		{"GET", "/entities/3/principals/4/permissions", 204},
		{"GET", "/entities/3/delivery-packages/7/items/" + item + "/comments", 204},
		{"POST", "/entities/3/delivery-packages/7/items/" + item + "/comments", 204},
		{"DELETE", "/entities/3/delivery-packages/7/items/" + item + "/comments", 404},
		{"GET", "/entities/3/delivery-packages/bad/items/" + item + "/comments", 404},
		{"PATCH", "/entities/3/delivery-packages/7", 404},
		{"GET", "/entities/3/channels", 204},
		{"PUT", "/channels/7/messages/8/saved", 204},
		{"GET", "/tenants/1/principals/5/permissions", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			called := false
			h := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))
			out := httptest.NewRecorder()
			h.ServeHTTP(out, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.status, out.Code, out.Body.String())
			require.Equal(t, tc.status == 204, called)
			require.Equal(t, "private, no-store", out.Header().Get("Cache-Control"))
		})
	}
}
