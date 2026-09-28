package iamintegration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposedSCIMTenantProjectionAndImmutableLogin(t *testing.T) {
	for _, prefix := range []string{"", "projection_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			f := newIAMTenantFixtureWithDatabase(t, app.Database{Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "projection.sqlite"), Writable: true, Readable: true}, prefix)
			response := f.request(t, http.MethodPost, "/service-accounts", iamTenantA, f.tokenA, map[string]any{"login_name": "projection-actor", "display_name": "Projection actor"}, nil)
			requireStatus(t, http.StatusCreated, response)
			actor := decodeEnvelope[identity.ServiceAccount](t, response)
			scim := f.actorProvisioningService(t, nil)
			directory, err := scim.CreateDirectory(t.Context(), iamTenantA, "Projection directory", &actor.ID)
			require.NoError(t, err)
			credential, err := scim.CreateCredential(t.Context(), iamTenantA, directory.ID, "Projection credential", nil)
			require.NoError(t, err)
			auth, err := scim.Authenticate(t.Context(), "Bearer "+credential.Token)
			require.NoError(t, err)
			active := true
			input := provisioning.UserInput{Schemas: []string{provisioning.UserSchema}, UserName: "projection-user", DisplayName: "Global name", Emails: []provisioning.UserEmail{{Value: "global@example.test", Primary: true}}, Active: &active}
			user, err := scim.CreateUser(t.Context(), auth, input)
			require.NoError(t, err)
			id, err := guid.Parse(user.ID)
			require.NoError(t, err)
			identities, err := module.Resolve[identity.IdentityProvisioner](f.app.Modules().Services())
			require.NoError(t, err)
			before, err := identities.Get(t.Context(), iamTenantA, id)
			require.NoError(t, err)
			response = f.request(t, http.MethodPost, "/members", iamTenantA, f.tokenA, map[string]any{"subject": f.requesterB.ID.String(), "display_name": "Tenant editor", "email": f.requesterB.Email, "status": "active"}, nil)
			requireStatus(t, http.StatusCreated, response)
			response = f.request(t, http.MethodPost, "/roles", iamTenantA, f.tokenA, map[string]any{"name": "Tenant profile editors"}, nil)
			requireStatus(t, http.StatusCreated, response)
			role := decodeEnvelope[core.APIRole](t, response)
			response = f.request(t, http.MethodPut, "/roles/"+role.ID.String()+"/permissions/user_update", 0, f.tokenA, nil, nil)
			requireStatus(t, http.StatusOK, response)
			response = f.request(t, http.MethodPut, "/roles/"+role.ID.String()+"/members/"+f.requesterB.ID.String(), 0, f.tokenA, nil, nil)
			requireStatus(t, http.StatusOK, response)
			response = f.request(t, http.MethodPatch, "/principals/"+user.ID, iamTenantA, f.requester, map[string]any{"email": "forbidden@example.test"}, nil)
			requireStatus(t, http.StatusForbidden, response)
			response = f.request(t, http.MethodPatch, "/members/"+user.ID, iamTenantA, f.requester, map[string]any{"email": "human-local@example.test", "display_name": "Human local"}, nil)
			requireStatus(t, http.StatusOK, response)
			projection, err := identities.GetProvisioned(t.Context(), iamTenantA, id)
			require.NoError(t, err)
			assert.Equal(t, "human-local@example.test", projection.Email)
			assert.Equal(t, "Human local", projection.DisplayName)
			input.DisplayName, input.Emails[0].Value = "Tenant name", "local@example.test"
			user, err = scim.ReplaceUser(t.Context(), auth, id, input, 1)
			require.NoError(t, err)
			assert.Equal(t, "Tenant name", user.DisplayName)
			assert.Equal(t, "local@example.test", user.Emails[0].Value)
			after, err := identities.Get(t.Context(), iamTenantA, id)
			require.NoError(t, err)
			assert.Equal(t, before, after, "tenant contact edits cannot mutate the global account")
			response = f.request(t, http.MethodGet, "/scim/v2/Users?filter=emails.value%20eq%20%22local@example.test%22", 0, credential.Token, nil, nil)
			requireStatus(t, http.StatusOK, response)
			assert.Contains(t, string(response.Body), user.ID)
			path := "/scim/v2/Users/" + user.ID
			input.UserName = "forbidden-rename"
			response = f.request(t, http.MethodPut, path, 0, credential.Token, input, nil)
			requireStatus(t, http.StatusBadRequest, response)
			assert.Contains(t, string(response.Body), `"scimType":"mutability"`)
			response = f.request(t, http.MethodPatch, path, 0, credential.Token, map[string]any{"schemas": []string{provisioning.PatchSchema}, "Operations": []map[string]any{{"op": "replace", "path": "displayName", "value": "Must roll back"}, {"op": "replace", "path": "userName", "value": "forbidden-rename"}}}, nil)
			requireStatus(t, http.StatusBadRequest, response)
			current, err := scim.GetUser(t.Context(), auth, id)
			require.NoError(t, err)
			assert.Equal(t, user, current)
			response = f.request(t, http.MethodPut, path, 0, credential.Token, map[string]any{"schemas": []string{provisioning.UserSchema}}, nil)
			requireStatus(t, http.StatusBadRequest, response)
			input.UserName, input.DisplayName, input.Name, input.Emails = "projection-user", "", provisioning.UserName{}, nil
			user, err = scim.ReplaceUser(t.Context(), auth, id, input, 2)
			require.NoError(t, err)
			assert.Equal(t, input.UserName, user.DisplayName)
			assert.Empty(t, user.Emails)
			require.NoError(t, f.db.Table(f.table("iam_principals")).Where("id = ?", id).Update("status", "disabled").Error)
			response = f.request(t, http.MethodPatch, path, 0, credential.Token, map[string]any{"schemas": []string{provisioning.PatchSchema}, "Operations": []map[string]any{{"op": "replace", "path": "displayName", "value": "Suspended local name"}}}, nil)
			requireStatus(t, http.StatusOK, response)
			projection, err = identities.GetProvisioned(t.Context(), iamTenantA, id)
			require.NoError(t, err)
			assert.Equal(t, "active", projection.MembershipStatus, "profile-only PATCH cannot change tenant admission")
			assert.Equal(t, "disabled", projection.Status)
			require.NoError(t, f.db.Table(f.table("iam_principals")).Where("id = ?", id).Update("status", "active").Error)

			var mu sync.Mutex
			var pushed provisioning.UserResource
			var deleted int
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodDelete {
					deleted++
					w.WriteHeader(http.StatusNoContent)
					return
				}
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&pushed))
				w.Header().Set("Content-Type", provisioning.SCIMContentType)
				_, _ = w.Write([]byte(`{"id":"projection-remote"}`))
			}))
			t.Cleanup(remote.Close)
			target, err := scim.CreateTarget(t.Context(), iamTenantA, "Projection target", remote.URL, "remote-token", &actor.ID)
			require.NoError(t, err)
			targetID, err := guid.Parse(target.Target.ID)
			require.NoError(t, err)
			_, err = scim.PushResource(t.Context(), iamTenantA, targetID, provisioning.ResourceUser, id)
			require.NoError(t, err)
			active = false
			_, err = scim.ReplaceUser(t.Context(), auth, id, input, 4)
			require.NoError(t, err)
			_, err = scim.PushResource(t.Context(), iamTenantA, targetID, provisioning.ResourceUser, id)
			require.NoError(t, err)
			mu.Lock()
			assert.False(t, pushed.Active)
			mu.Unlock()
			after, err = identities.Get(t.Context(), iamTenantA, id)
			require.NoError(t, err)
			assert.Equal(t, "active", after.Status)
			require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Where("tenant_id = ? AND principal_id = ?", iamTenantA, id).Delete(nil).Error)
			if prefix == "" {
				_, err = scim.PushResource(t.Context(), iamTenantA, targetID, provisioning.ResourceUser, id)
				assert.ErrorIs(t, err, provisioning.ErrResourceMissing)
			} else {
				_, err = scim.SyncTarget(t.Context(), iamTenantA, targetID)
				require.NoError(t, err)
			}
			mu.Lock()
			assert.Equal(t, 1, deleted)
			mu.Unlock()
		})
	}
}
