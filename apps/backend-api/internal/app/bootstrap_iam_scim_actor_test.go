package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestComposedSCIMServiceAccountBindings(t *testing.T) {
	for _, prefix := range []string{"", "actor_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			f := newIAMTenantFixtureWithDatabase(t, Database{
				Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "actors.sqlite"), Writable: true, Readable: true,
			}, prefix)
			f.runSCIMActorLifecycle(t)
		})
	}
}

func (f *iamTenantFixture) actorProvisioningService(t *testing.T, appender audit.Appender) *provisioning.Service {
	t.Helper()
	services := f.app.modules.Services()
	ids, err := module.Resolve[guid.Source](services)
	require.NoError(t, err)
	identities, err := module.Resolve[identity.IdentityProvisioner](services)
	require.NoError(t, err)
	groups, err := module.Resolve[organization.GroupProvisioner](services)
	require.NoError(t, err)
	actors, err := module.Resolve[identity.ServiceAccountValidator](services)
	require.NoError(t, err)
	if appender == nil {
		appender, err = module.Resolve[audit.Appender](services)
		require.NoError(t, err)
	}
	config := provisioning.Config{
		EncryptionKey:          f.app.config.Provisioning.EncryptionKey,
		OutboundAllowPlaintext: true,
		OutboundAllowNetworks:  []string{"127.0.0.0/8", "::1/128"},
	}
	keys, err := provisioning.ResolveKeyring(config)
	require.NoError(t, err)
	fields, err := module.Resolve[core.AuthorizationEvaluator](services)
	require.NoError(t, err)
	outbound, err := provisioning.ResolveOutboundPolicy(config)
	require.NoError(t, err)
	scim := provisioning.NewService(f.app.runtime, appender, ids, identities, groups, actors, fields, config, keys, outbound)
	// A service built outside the module never went through injection, so it
	// binds the owners' published names itself.
	prefix := f.app.runtime.TablePrefix()
	require.NoError(t, scim.BindForeignTables(core.PublishedAccessTableNames(prefix),
		identity.PublishedPrincipalTableNames(prefix), organization.PublishedDirectoryTableNames(prefix)))
	return scim
}

func (f *iamTenantFixture) runSCIMActorLifecycle(t *testing.T) {
	t.Helper()
	createAccount := func(tenant guid.ID, token, login string) identity.ServiceAccount {
		response := f.request(t, http.MethodPost, "/service-accounts", tenant, token,
			map[string]any{"login_name": login, "display_name": login}, nil)
		requireStatus(t, http.StatusCreated, response)
		return decodeEnvelope[identity.ServiceAccount](t, response)
	}
	first := createAccount(iamTenantA, f.tokenA, "scim-actor-first")
	second := createAccount(iamTenantA, f.tokenA, "scim-actor-second")
	foreign := createAccount(iamTenantB, f.tokenB, "scim-actor-foreign")
	var remoteCalls atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		assert.Equal(t, "Bearer actor-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", provisioning.SCIMContentType)
		_, _ = io.WriteString(w, `{"id":"remote-actor-user"}`)
	}))
	t.Cleanup(remote.Close)

	for _, invalid := range []guid.ID{f.adminA.ID, foreign.ID, 999999} {
		beforeAudit := f.auditCount(t, iamTenantA)
		beforeDirectories := f.count(t, "iam_scim_directories", "tenant_id = ?", iamTenantA)
		beforeTargets := f.count(t, "iam_scim_targets", "tenant_id = ?", iamTenantA)
		response := f.request(t, http.MethodPost, "/scim-directories", iamTenantA, f.tokenA,
			map[string]any{"name": "invalid actor", "service_account_id": invalid.String()}, nil)
		requireStatus(t, http.StatusUnprocessableEntity, response)
		response = f.request(t, http.MethodPost, "/scim-targets", iamTenantA, f.tokenA,
			map[string]any{"name": "invalid actor", "base_url": remote.URL, "bearer_token": "actor-test", "service_account_id": invalid.String()}, nil)
		requireStatus(t, http.StatusUnprocessableEntity, response)
		assert.Equal(t, beforeAudit, f.auditCount(t, iamTenantA))
		assert.Equal(t, beforeDirectories, f.count(t, "iam_scim_directories", "tenant_id = ?", iamTenantA))
		assert.Equal(t, beforeTargets, f.count(t, "iam_scim_targets", "tenant_id = ?", iamTenantA))
	}
	response := f.request(t, http.MethodPost, "/scim-directories", iamTenantA, f.tokenA,
		map[string]any{"name": "Bound directory", "service_account_id": first.ID.String()}, nil)
	requireStatus(t, http.StatusCreated, response)
	directory := decodeEnvelope[provisioning.Directory](t, response)
	require.Equal(t, &first.ID, directory.ServiceAccountID)
	response = f.request(t, http.MethodPost, "/scim-directories/"+directory.ID.String()+"/credentials", iamTenantA, f.tokenA,
		map[string]any{"name": "Bound credential"}, nil)
	requireStatus(t, http.StatusCreated, response)
	credential := decodeEnvelope[provisioning.CredentialSecret](t, response)
	response = f.request(t, http.MethodPost, "/scim-targets", iamTenantA, f.tokenA,
		map[string]any{"name": "Bound target", "base_url": remote.URL, "bearer_token": "actor-test", "service_account_id": first.ID.String()}, nil)
	requireStatus(t, http.StatusCreated, response)
	target := decodeEnvelope[provisioning.TargetSecret](t, response).Target
	require.Equal(t, &first.ID, target.ServiceAccountID)
	targetID, err := guid.Parse(target.ID)
	require.NoError(t, err)
	service := f.actorProvisioningService(t, nil)
	auth, err := service.Authenticate(t.Context(), "Bearer "+credential.Token)
	require.NoError(t, err)
	assert.Equal(t, first.ID, auth.PrincipalID)
	response = f.request(t, http.MethodPost, "/scim/v2/Groups", 0, credential.Token,
		provisioning.GroupInput{Schemas: []string{provisioning.GroupSchema}, DisplayName: "Actor audit group"}, nil)
	requireStatus(t, http.StatusCreated, response)
	assert.Equal(t, int64(1), f.count(t, "iam_audit_events", "tenant_id = ? AND event_type = ? AND principal_id = ?",
		iamTenantA, "scim_group_created", first.ID))

	directoryBody := func(actor *guid.ID, status string, version int64) map[string]any {
		return map[string]any{"name": directory.Name, "status": status, "version": version, "service_account_id": actor}
	}
	targetBody := func(actor *guid.ID, status string, version int64) map[string]any {
		return map[string]any{"name": target.Name, "base_url": target.BaseURL, "status": status, "version": version, "service_account_id": actor}
	}
	directoryPath := "/scim-directories/" + directory.ID.String()
	targetPath := "/scim-targets/" + target.ID
	response = f.request(t, http.MethodPut, directoryPath, iamTenantA, f.tokenA, directoryBody(&second.ID, "active", directory.Version), nil)
	requireStatus(t, http.StatusOK, response)
	directory = decodeEnvelope[provisioning.Directory](t, response)
	assert.Equal(t, int64(2), directory.Version)
	assert.Equal(t, &second.ID, directory.ServiceAccountID)
	response = f.request(t, http.MethodPut, directoryPath, iamTenantA, f.tokenA, directoryBody(&first.ID, "active", 1), nil)
	requireStatus(t, http.StatusConflict, response)
	_, err = service.GetUser(t.Context(), auth, f.adminA.ID)
	assert.ErrorIs(t, err, provisioning.ErrUnauthorized, "retained auth cannot survive rebind")
	auth, err = service.Authenticate(t.Context(), "Bearer "+credential.Token)
	require.NoError(t, err)
	assert.Equal(t, second.ID, auth.PrincipalID)
	for _, spoofed := range []guid.ID{0, first.ID, f.adminA.ID} {
		forged := auth
		forged.PrincipalID = spoofed
		_, err = service.GetUser(t.Context(), forged, f.adminA.ID)
		assert.ErrorIs(t, err, provisioning.ErrUnauthorized)
	}
	response = f.request(t, http.MethodPut, targetPath, iamTenantA, f.tokenA, targetBody(&second.ID, "active", target.Version), nil)
	requireStatus(t, http.StatusOK, response)
	target = decodeEnvelope[provisioning.Target](t, response)
	assert.Equal(t, int64(2), target.Version)
	assert.Equal(t, &second.ID, target.ServiceAccountID)
	response = f.request(t, http.MethodPut, targetPath, iamTenantA, f.tokenA, targetBody(&first.ID, "active", 1), nil)
	requireStatus(t, http.StatusConflict, response)
	pushBody := map[string]any{"resource_type": "User", "resource_id": f.adminA.ID.String()}
	response = f.request(t, http.MethodPost, targetPath+"/push", iamTenantA, f.tokenA, pushBody, nil)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, int64(1), remoteCalls.Load())
	var pushAudit struct {
		PrincipalID guid.ID
		Detail      string
	}
	require.NoError(t, f.db.Table(f.table("iam_audit_events")).Where("tenant_id = ? AND event_type = ? AND target_id = ?",
		iamTenantA, "scim_target_pushed", f.adminA.ID).Take(&pushAudit).Error)
	assert.Equal(t, f.adminA.ID, pushAudit.PrincipalID, "preserve the initiating administrator")
	var pushDetail map[string]any
	require.NoError(t, json.Unmarshal([]byte(pushAudit.Detail), &pushDetail))
	assert.Equal(t, second.ID.String(), pushDetail["service_account_id"])

	assertRejected := func(t *testing.T) {
		t.Helper()
		beforeCalls, beforeAudit := remoteCalls.Load(), f.auditCount(t, iamTenantA)
		_, err := service.Authenticate(t.Context(), "Bearer "+credential.Token)
		assert.ErrorIs(t, err, provisioning.ErrUnauthorized)
		_, err = service.GetUser(t.Context(), auth, f.adminA.ID)
		assert.ErrorIs(t, err, provisioning.ErrUnauthorized)
		response := f.request(t, http.MethodGet, "/scim/v2/Users", 0, credential.Token, nil, nil)
		requireStatus(t, http.StatusUnauthorized, response)
		for _, operation := range []string{"push", "deprovision"} {
			response = f.request(t, http.MethodPost, targetPath+"/"+operation, iamTenantA, f.tokenA, pushBody, nil)
			requireStatus(t, http.StatusConflict, response)
		}
		_, err = service.SyncTarget(t.Context(), iamTenantA, targetID)
		assert.ErrorIs(t, err, provisioning.ErrUnauthorized)
		assert.Equal(t, beforeCalls, remoteCalls.Load(), "no remote side effects")
		assert.Equal(t, beforeAudit, f.auditCount(t, iamTenantA), "no rejected-operation audit writes")
	}
	for _, state := range []struct {
		table, column  string
		value, restore any
	}{
		{"iam_service_accounts", "expires_at", time.Now().UTC().Add(-time.Hour), nil},
		{"iam_service_accounts", "deleted_at", time.Now().UTC(), nil},
		{"iam_service_accounts", "status", "disabled", "active"},
		{"iam_principals", "status", "disabled", "active"},
		{"iam_principals", "activation_required", true, false},
		{"iam_tenant_members", "status", "disabled", "active"},
	} {
		require.True(t, t.Run(state.table+"/"+state.column, func(t *testing.T) {
			key := "principal_id"
			if state.table == "iam_principals" {
				key = "id"
			}
			query := f.db.Table(f.table(state.table)).Where(key+" = ?", second.ID)
			require.NoError(t, query.Update(state.column, state.value).Error)
			t.Cleanup(func() { require.NoError(t, query.Update(state.column, state.restore).Error) })
			assertRejected(t)
		}))
	}
	response = f.request(t, http.MethodDelete, "/service-accounts/"+second.ID.String()+"?version="+strconv.FormatInt(second.Version, 10), iamTenantA, f.tokenA, nil, nil)
	requireStatus(t, http.StatusOK, response)
	assertRejected(t)
	// An invalid old actor must not prevent an administrator from disabling or repairing a configuration.
	response = f.request(t, http.MethodPut, directoryPath, iamTenantA, f.tokenA, directoryBody(&second.ID, "disabled", directory.Version), nil)
	requireStatus(t, http.StatusOK, response)
	directory = decodeEnvelope[provisioning.Directory](t, response)
	response = f.request(t, http.MethodPut, targetPath, iamTenantA, f.tokenA, targetBody(&second.ID, "disabled", target.Version), nil)
	requireStatus(t, http.StatusOK, response)
	target = decodeEnvelope[provisioning.Target](t, response)
	response = f.request(t, http.MethodPut, directoryPath, iamTenantA, f.tokenA, directoryBody(&first.ID, "active", directory.Version), nil)
	requireStatus(t, http.StatusOK, response)
	directory = decodeEnvelope[provisioning.Directory](t, response)
	response = f.request(t, http.MethodPut, targetPath, iamTenantA, f.tokenA, targetBody(&first.ID, "active", target.Version), nil)
	requireStatus(t, http.StatusOK, response)
	target = decodeEnvelope[provisioning.Target](t, response)
	_, err = service.Authenticate(t.Context(), "Bearer "+credential.Token)
	require.NoError(t, err)
	response = f.request(t, http.MethodPost, targetPath+"/push", iamTenantA, f.tokenA, pushBody, nil)
	requireStatus(t, http.StatusOK, response)
	synced, err := service.SyncTarget(t.Context(), iamTenantA, targetID)
	require.NoError(t, err)
	assert.Equal(t, 1, synced)
	assert.Equal(t, int64(1), f.count(t, "iam_audit_events", "tenant_id = ? AND event_type = ? AND principal_id = ?",
		iamTenantA, "scim_target_pushed", first.ID), "background sync records the actual actor")
	require.NoError(t, service.DeprovisionResource(t.Context(), iamTenantA, targetID, "User", f.adminA.ID))
	assert.Equal(t, int64(1), f.count(t, "iam_audit_events", "tenant_id = ? AND event_type = ? AND principal_id = ?",
		iamTenantA, "scim_target_deprovisioned", first.ID))

	response = f.request(t, http.MethodPut, directoryPath, iamTenantA, f.tokenA, directoryBody(nil, "disabled", directory.Version), nil)
	requireStatus(t, http.StatusOK, response)
	assert.Nil(t, decodeEnvelope[provisioning.Directory](t, response).ServiceAccountID)
	response = f.request(t, http.MethodPut, targetPath, iamTenantA, f.tokenA, targetBody(nil, "disabled", target.Version), nil)
	requireStatus(t, http.StatusOK, response)
	assert.Nil(t, decodeEnvelope[provisioning.Target](t, response).ServiceAccountID)
	assert.Equal(t, int64(1), f.count(t, "iam_scim_directories", "id = ? AND service_account_id IS NULL", directory.ID))
	assert.Equal(t, int64(1), f.count(t, "iam_scim_targets", "id = ? AND service_account_id IS NULL", targetID))
}

func TestComposedSCIMBindingAuditRollback(t *testing.T) {
	f := newIAMTenantFixture(t)
	response := f.request(t, http.MethodPost, "/service-accounts", iamTenantA, f.tokenA,
		map[string]any{"login_name": "rollback-actor", "display_name": "Rollback actor"}, nil)
	requireStatus(t, http.StatusCreated, response)
	account := decodeEnvelope[identity.ServiceAccount](t, response)
	service := f.actorProvisioningService(t, nil)
	directory, err := service.CreateDirectory(t.Context(), iamTenantA, "Rollback directory", nil)
	require.NoError(t, err)
	target, err := service.CreateTarget(t.Context(), iamTenantA, "Rollback target", "https://scim.example.test", "test-token", nil)
	require.NoError(t, err)
	targetID, err := guid.Parse(target.Target.ID)
	require.NoError(t, err)
	failure := errors.New("audit unavailable")
	failing := f.actorProvisioningService(t, func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure })
	_, err = failing.ReplaceDirectory(t.Context(), iamTenantA, directory.ID, directory.Name, directory.Status, directory.Version, &account.ID)
	require.ErrorIs(t, err, failure)
	_, err = failing.ReplaceTarget(t.Context(), iamTenantA, targetID, target.Target.Name, target.Target.BaseURL, target.Target.Status, "", target.Target.Version, &account.ID)
	require.ErrorIs(t, err, failure)
	assert.Equal(t, int64(1), f.count(t, "iam_scim_directories", "id = ? AND service_account_id IS NULL AND version = 1", directory.ID))
	assert.Equal(t, int64(1), f.count(t, "iam_scim_targets", "id = ? AND service_account_id IS NULL AND version = 1", targetID))
}
