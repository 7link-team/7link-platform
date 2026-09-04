package provisioning

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scimProviderRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    string
}

type scimProviderEndpoint struct {
	mu       sync.Mutex
	requests []scimProviderRequest
	status   int
	echo     bool
	body     string
}

func newSCIMProviderEndpoint(t *testing.T, status int, body string) (*scimProviderEndpoint, *httptest.Server) {
	t.Helper()
	provider := &scimProviderEndpoint{status: status, body: body}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		provider.mu.Lock()
		provider.requests = append(provider.requests, scimProviderRequest{Method: r.Method, Path: r.URL.Path, Headers: r.Header.Clone(), Body: string(data)})
		status, body, echo := provider.status, provider.body, provider.echo
		provider.mu.Unlock()
		w.Header().Set("Content-Type", SCIMContentType)
		w.WriteHeader(status)
		if echo {
			resourceType := "User"
			if strings.Contains(r.URL.Path, "/Groups/") {
				resourceType = "Group"
			}
			body = fmt.Sprintf(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:%s"],"id":"remote-%s"}`, resourceType, path.Base(r.URL.Path))
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return provider, server
}

func (p *scimProviderEndpoint) last(t *testing.T) scimProviderRequest {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.NotEmpty(t, p.requests)
	return p.requests[len(p.requests)-1]
}

func (p *scimProviderEndpoint) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *scimProviderEndpoint) configure(status int, body string, echo bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.body, p.echo = status, body, echo
}

func TestPushResourceLocksSerializeAndCleanUp(t *testing.T) {
	service := &Service{pushLocks: make(map[string]*pushLock)}
	targetID, resourceID := testID("lock-target"), testID("lock-resource")
	unlockFirst := service.lockPushResource(targetID, ResourceUser, resourceID)

	acquired := make(chan struct{})
	released := make(chan struct{})
	go func() {
		unlockSecond := service.lockPushResource(targetID, ResourceUser, resourceID)
		close(acquired)
		unlockSecond()
		close(released)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		service.pushMu.Lock()
		refs := service.pushLocks[targetID.String()+"\x00"+ResourceUser+"\x00"+resourceID.String()].refs
		service.pushMu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second caller did not register for the resource lock")
		}
		runtime.Gosched()
	}
	select {
	case <-acquired:
		t.Fatal("same-resource caller acquired the lock concurrently")
	default:
	}

	unlockFirst()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("second caller did not acquire and release the resource lock")
	}
	service.pushMu.Lock()
	defer service.pushMu.Unlock()
	assert.Empty(t, service.pushLocks)
}

func TestTargetCRUDLifecycleAndValidation(t *testing.T) {
	env := newProvisioningEnvironment(t)
	ctx := t.Context()
	created, err := env.service.CreateTarget(ctx, testID("tenant-a"), "Okta", "https://scim.example.test/v2", "bearer-secret")
	require.NoError(t, err)
	assert.Equal(t, TargetActive, created.Target.Status)
	assert.Equal(t, int64(1), created.Target.Version)

	var stored model.Target
	require.NoError(t, env.db.Table(env.prefix+stored.TableName()).Where("id = ?", created.Target.ID).Take(&stored).Error)
	assert.NotContains(t, stored.BearerTokenCiphertext, "bearer-secret")
	plain, err := env.service.decryptToken(stored.ID, stored.BearerTokenCiphertext)
	require.NoError(t, err)
	assert.Equal(t, "bearer-secret", plain)

	_, err = env.service.CreateTarget(ctx, testID("tenant-a"), "okta", "https://other.example.test/v2", "x")
	assert.ErrorIs(t, err, ErrTargetName)
	targets, err := env.service.ListTargets(ctx, testID("tenant-a"))
	require.NoError(t, err)
	require.Len(t, targets, 1)

	updated, err := env.service.ReplaceTarget(ctx, testID("tenant-a"), parseGUID(created.Target.ID), "Okta Prod", "https://scim.example.test/v2", TargetActive, "rotated-secret", 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)
	_, err = env.service.ReplaceTarget(ctx, testID("tenant-a"), parseGUID(created.Target.ID), "Okta Prod", "https://scim.example.test/v2", TargetActive, "", 1)
	assert.ErrorIs(t, err, ErrTargetVersion)
	noop, err := env.service.ReplaceTarget(ctx, testID("tenant-a"), parseGUID(created.Target.ID), "Okta Prod", "https://scim.example.test/v2", TargetActive, "", 2)
	require.NoError(t, err)
	assert.Equal(t, int64(2), noop.Version)
	require.NoError(t, env.service.DeleteTarget(ctx, testID("tenant-a"), parseGUID(created.Target.ID)))
	assert.ErrorIs(t, env.service.DeleteTarget(ctx, testID("tenant-a"), parseGUID(created.Target.ID)), ErrTargetMissing)

	for _, input := range []struct{ name, url, token string }{
		{"bad", "not-a-url", "token"}, {"bad", "ftp://example.test", "token"},
		{"bad", "https://example.test", ""}, {"", "https://example.test", "token"},
	} {
		_, err := env.service.CreateTarget(ctx, testID("tenant-a"), input.name, input.url, input.token)
		assert.ErrorIs(t, err, ErrInvalidTarget)
	}
	_, err = env.service.ListTargets(ctx, 0)
	assert.ErrorIs(t, err, ErrInvalidTarget)
	assert.ErrorIs(t, env.service.DeleteTarget(ctx, 0, testID("target")), ErrInvalidTarget)
	assert.ErrorIs(t, env.service.DeprovisionResource(ctx, 0, testID("target"), ResourceUser, testID("user")), ErrInvalidTarget)
	_, err = env.service.ReplaceTarget(ctx, testID("tenant-a"), testID("missing"), "x", "https://example.test", TargetActive, "", 1)
	assert.ErrorIs(t, err, ErrTargetMissing)
}

func TestPushUserGroupDeprovisionAndSync(t *testing.T) {
	env := newProvisioningEnvironment(t)
	provider, server := newSCIMProviderEndpoint(t, http.StatusOK, "")
	provider.configure(http.StatusOK, "", true)
	ctx := t.Context()
	target, err := env.service.CreateTarget(ctx, testID("tenant-a"), "Okta", server.URL, "bearer-secret")
	require.NoError(t, err)

	synced, err := env.service.SyncTarget(ctx, testID("tenant-a"), parseGUID(target.Target.ID))
	require.NoError(t, err)
	assert.Zero(t, synced)
	alice, err := env.service.CreateUser(ctx, env.auth, activeUserInput("ext-alice", "alice", "alice@example.test"))
	require.NoError(t, err)
	bob, err := env.service.CreateUser(ctx, env.auth, activeUserInput("ext-bob", "bob", "bob@example.test"))
	require.NoError(t, err)

	result, err := env.service.PushResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(alice.ID))
	require.NoError(t, err)
	assert.Equal(t, "remote-"+alice.ID, result.RemoteID)
	request := provider.last(t)
	assert.Equal(t, http.MethodPut, request.Method)
	assert.Equal(t, "/Users/"+alice.ID, request.Path)
	assert.Equal(t, "Bearer bearer-secret", request.Headers.Get("Authorization"))
	assert.Contains(t, request.Body, `"userName":"alice"`)
	_, err = env.service.PushResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(bob.ID))
	require.NoError(t, err)

	group, err := env.service.CreateGroup(ctx, env.auth, GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Engineering", Members: []SCIMGroupMember{{Value: alice.ID}, {Value: bob.ID}}})
	require.NoError(t, err)
	groupResult, err := env.service.PushResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceGroup, parseGUID(group.ID))
	require.NoError(t, err)
	assert.Equal(t, "remote-"+group.ID, groupResult.RemoteID)
	request = provider.last(t)
	assert.Contains(t, request.Body, `"value":"remote-`+alice.ID+`"`)
	assert.Contains(t, request.Body, `"value":"remote-`+bob.ID+`"`)

	before := provider.count()
	synced, err = env.service.SyncTarget(ctx, testID("tenant-a"), parseGUID(target.Target.ID))
	require.NoError(t, err)
	assert.Equal(t, 3, synced)
	assert.Greater(t, provider.count(), before)

	require.NoError(t, env.service.DeprovisionResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(alice.ID)))
	assert.Equal(t, http.MethodDelete, provider.last(t).Method)
	assert.ErrorIs(t, env.service.DeprovisionResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(alice.ID)), ErrDeprovisionMissing)
	_, err = env.service.PushResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(alice.ID))
	require.NoError(t, err)
	assert.Contains(t, provider.last(t).Path, "remote-"+alice.ID)
}

func TestTargetRemoteAndKeyFailurePaths(t *testing.T) {
	env := newProvisioningEnvironment(t)
	ctx := t.Context()
	user, err := env.service.CreateUser(ctx, env.auth, activeUserInput("ext-alice", "alice", "alice@example.test"))
	require.NoError(t, err)

	provider, server := newSCIMProviderEndpoint(t, http.StatusInternalServerError, strings.Repeat("remote failure\n", 100))
	target, err := env.service.CreateTarget(ctx, testID("tenant-a"), "Failing", server.URL, "token")
	require.NoError(t, err)
	_, err = env.service.PushResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrRemoteResponse)

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	down, err := env.service.CreateTarget(ctx, testID("tenant-a"), "Down", closedURL, "token")
	require.NoError(t, err)
	_, err = env.service.PushResource(ctx, testID("tenant-a"), parseGUID(down.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrRemoteUnavailable)

	provider.configure(http.StatusOK, `{"id":"remote-user"}`, false)
	_, err = env.service.PushResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	require.NoError(t, err)
	provider.configure(http.StatusNotFound, `{"detail":"gone"}`, false)
	require.NoError(t, env.service.DeprovisionResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID)))

	disabled, err := env.service.ReplaceTarget(ctx, testID("tenant-a"), parseGUID(target.Target.ID), "Failing", server.URL, TargetDisabled, "", target.Target.Version)
	require.NoError(t, err)
	_, err = env.service.PushResource(ctx, testID("tenant-a"), parseGUID(disabled.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrTargetDisabled)
	assert.ErrorIs(t, env.service.DeprovisionResource(ctx, testID("tenant-a"), parseGUID(disabled.ID), ResourceUser, parseGUID(user.ID)), ErrTargetDisabled)

	noKey := NewService(env.runtime, env.service.appender, newProvisioningTestIDs(9000), env.service.identity, env.service.groups, Config{}, nil)
	_, err = noKey.CreateTarget(ctx, testID("tenant-a"), "No Key", "https://example.test", "token")
	assert.ErrorIs(t, err, ErrTargetKeyMissing)
	_, err = noKey.PushResource(ctx, testID("tenant-a"), parseGUID(down.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrTargetKeyMissing)

	_, err = env.service.PushResource(ctx, testID("tenant-a"), parseGUID(target.Target.ID), "Widget", parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrInvalidTarget)
	_, err = env.service.SyncTarget(ctx, 0, parseGUID(target.Target.ID))
	assert.ErrorIs(t, err, ErrInvalidTarget)
	_, err = env.service.SyncTarget(ctx, testID("tenant-a"), testID("missing"))
	assert.ErrorIs(t, err, ErrTargetMissing)
	_, err = env.service.SyncTarget(ctx, testID("tenant-a"), parseGUID(disabled.ID))
	assert.ErrorIs(t, err, ErrTargetDisabled)
}

func TestTargetDeprovisionRemoteFailuresAndAuditRollback(t *testing.T) {
	env := newProvisioningEnvironment(t)
	provider, server := newSCIMProviderEndpoint(t, http.StatusOK, `{"id":"remote-user"}`)
	target, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Failures", server.URL, "secret")
	require.NoError(t, err)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("failure-user", "failure-user", "failure@example.test"))
	require.NoError(t, err)
	_, err = env.service.PushResource(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	require.NoError(t, err)

	provider.configure(http.StatusInternalServerError, "delete rejected", false)
	err = env.service.DeprovisionResource(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrRemoteResponse)
	provider.configure(http.StatusOK, "", false)
	env.auditErr = errors.New("audit failed")
	err = env.service.DeprovisionResource(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorContains(t, err, "audit failed")
	var deletedCount int64
	require.NoError(t, env.db.Table(env.prefix+"iam_scim_target_resources").
		Where("target_id = ? AND resource_type = ? AND resource_id = ? AND deleted_at IS NOT NULL", target.Target.ID, ResourceUser, user.ID).Count(&deletedCount).Error)
	assert.Zero(t, deletedCount)

	env.auditErr = errors.New("push audit failed")
	second, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("second-failure", "second-failure", "second-failure@example.test"))
	assert.ErrorContains(t, err, "push audit failed")
	// The prior create is expected to fail because audit is part of its transaction.
	assert.Empty(t, second.ID)
	env.auditErr = nil
	second, err = env.service.CreateUser(t.Context(), env.auth, activeUserInput("second-failure", "second-failure", "second-failure@example.test"))
	require.NoError(t, err)
	env.auditErr = errors.New("push audit failed")
	_, err = env.service.PushResource(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(second.ID))
	assert.ErrorContains(t, err, "push audit failed")
	var count int64
	require.NoError(t, env.db.Table(env.prefix+"iam_scim_target_resources").Where("target_id = ? AND resource_id = ?", target.Target.ID, second.ID).Count(&count).Error)
	assert.Zero(t, count)
}

func TestSyncTargetDeprovisionsMissingLocalResource(t *testing.T) {
	env := newProvisioningEnvironment(t)
	provider, server := newSCIMProviderEndpoint(t, http.StatusOK, `{"id":"remote-missing"}`)
	target, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Drift", server.URL, "secret")
	require.NoError(t, err)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("drift", "drift", "drift@example.test"))
	require.NoError(t, err)
	_, err = env.service.PushResource(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	require.NoError(t, err)
	require.NoError(t, env.db.Table(env.prefix+"iam_principals").Where("id = ?", user.ID).Delete(&testPrincipal{}).Error)

	synced, err := env.service.SyncTarget(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID))
	require.NoError(t, err)
	assert.Zero(t, synced)
	assert.Equal(t, http.MethodDelete, provider.last(t).Method)
}

func TestPushRejectsMalformedStoredCiphertextAndMissingResources(t *testing.T) {
	env := newProvisioningEnvironment(t)
	provider, server := newSCIMProviderEndpoint(t, http.StatusOK, `{}`)
	_ = provider
	target, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Malformed", server.URL, "secret")
	require.NoError(t, err)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("cipher", "cipher", "cipher@example.test"))
	require.NoError(t, err)
	require.NoError(t, env.db.Table(env.prefix+"iam_scim_targets").Where("id = ?", target.Target.ID).Update("bearer_token_ciphertext", "v9.invalid").Error)
	_, err = env.service.PushResource(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorContains(t, err, "unsupported provisioning ciphertext")

	valid, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Valid", server.URL, "secret")
	require.NoError(t, err)
	_, err = env.service.PushResource(t.Context(), testID("tenant-a"), parseGUID(valid.Target.ID), ResourceUser, testID("missing"))
	assert.ErrorIs(t, err, ErrResourceMissing)
	_, err = env.service.PushResource(t.Context(), testID("tenant-a"), testID("missing"), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrTargetMissing)
	assert.ErrorIs(t, env.service.DeprovisionResource(t.Context(), testID("tenant-a"), parseGUID(valid.Target.ID), ResourceUser, parseGUID(user.ID)), ErrDeprovisionMissing)
	assert.ErrorIs(t, env.service.DeprovisionResource(t.Context(), testID("tenant-a"), testID("missing"), ResourceUser, parseGUID(user.ID)), ErrTargetMissing)
}

type failingTargetIdentity struct {
	identity.IdentityProvisioner
	err error
}

func (p failingTargetIdentity) Get(context.Context, guid.ID, guid.ID) (identity.Principal, error) {
	return identity.Principal{}, p.err
}

type failingTargetGroups struct {
	organization.GroupProvisioner
	getErr     error
	membersErr error
}

func (p failingTargetGroups) Get(ctx context.Context, tenantID, id guid.ID) (organization.Group, error) {
	if p.getErr != nil {
		return organization.Group{}, p.getErr
	}
	return p.GroupProvisioner.Get(ctx, tenantID, id)
}

func (p failingTargetGroups) ListMembers(ctx context.Context, tenantID, id guid.ID) ([]organization.GroupMember, error) {
	if p.membersErr != nil {
		return nil, p.membersErr
	}
	return p.GroupProvisioner.ListMembers(ctx, tenantID, id)
}

func TestBuildRemoteResourcePropagatesProviderFailures(t *testing.T) {
	env := newProvisioningEnvironment(t)
	providerErr := errors.New("provider read failed")
	originalIdentity := env.service.identity
	env.service.identity = failingTargetIdentity{IdentityProvisioner: originalIdentity, err: providerErr}
	_, err := env.service.buildRemoteResource(t.Context(), testID("tenant-a"), testID("target"), ResourceUser, testID("user"))
	assert.ErrorIs(t, err, providerErr)
	env.service.identity = originalIdentity

	originalGroups := env.service.groups
	env.service.groups = failingTargetGroups{GroupProvisioner: originalGroups, getErr: providerErr}
	_, err = env.service.buildRemoteResource(t.Context(), testID("tenant-a"), testID("target"), ResourceGroup, testID("group"))
	assert.ErrorIs(t, err, providerErr)
	group, err := env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Provider Group"})
	require.NoError(t, err)
	env.service.groups = failingTargetGroups{GroupProvisioner: originalGroups, membersErr: providerErr}
	_, err = env.service.buildRemoteResource(t.Context(), testID("tenant-a"), testID("target"), ResourceGroup, parseGUID(group.ID))
	assert.ErrorIs(t, err, providerErr)
	env.service.groups = originalGroups
	_, err = env.service.buildRemoteResource(t.Context(), testID("tenant-a"), testID("target"), "Widget", testID("resource"))
	assert.ErrorIs(t, err, ErrInvalidTarget)
}

func TestTargetEncryptionConfigAndCipherErrors(t *testing.T) {
	key, err := ParseEncryptionKey("")
	require.NoError(t, err)
	assert.Nil(t, key)
	_, err = ParseEncryptionKey("too-short")
	assert.Error(t, err)
	encoded := base64.RawStdEncoding.EncodeToString(testProvisioningKey())
	key, err = ParseEncryptionKey(encoded)
	require.NoError(t, err)
	assert.Equal(t, testProvisioningKey(), key)

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "provisioning.key")
	require.NoError(t, os.WriteFile(keyFile, []byte(encoded+"\n"), 0o600))
	resolved, err := ResolveEncryptionKey(Config{EncryptionKeyFile: keyFile})
	require.NoError(t, err)
	assert.Equal(t, key, resolved)
	_, err = ResolveEncryptionKey(Config{EncryptionKey: encoded, EncryptionKeyFile: keyFile})
	assert.Error(t, err)
	_, err = ResolveEncryptionKey(Config{EncryptionKeyFile: filepath.Join(dir, "missing.key")})
	assert.Error(t, err)

	env := newProvisioningEnvironment(t)
	for _, encoded := range []string{"v9.garbage", "v1.!!not-base64!!", "v1.AA"} {
		_, err := env.service.decryptToken(testID("target"), encoded)
		assert.Error(t, err)
	}
	assert.False(t, validTargetURL("https://user:pass@example.test"))
	assert.False(t, validTargetURL("https://example.test/#fragment"))
	assert.Equal(t, strings.Repeat("x", 500)+"...", trimRemoteDetail([]byte(strings.Repeat("x", 501))))
}
