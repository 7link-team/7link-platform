package provisioning

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBulkCreatesReferencedResourcesAndStopsOnFailures(t *testing.T) {
	env := newProvisioningEnvironment(t)
	userData, err := json.Marshal(activeUserInput("bulk-user", "bulk-user", "bulk@example.test"))
	require.NoError(t, err)
	groupData, err := json.Marshal(GroupInput{Schemas: []string{GroupSchema}, ExternalID: "bulk-group", DisplayName: "Bulk Group", Members: []SCIMGroupMember{{Value: "bulkId:user-one"}}})
	require.NoError(t, err)

	response, err := env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{
		{Method: http.MethodPost, BulkID: "user-one", Path: "/Users", Data: userData},
		{Method: http.MethodPost, BulkID: "group-one", Path: "/Groups", Data: groupData},
	}})
	require.NoError(t, err)
	require.Len(t, response.Operations, 2)
	assert.Equal(t, "201", response.Operations[0].Status)
	assert.Equal(t, "201", response.Operations[1].Status)
	userID := pathID(response.Operations[0].Location)
	groupID := pathID(response.Operations[1].Location)
	group, err := env.service.GetGroup(t.Context(), env.auth, parseGUID(groupID))
	require.NoError(t, err)
	require.Len(t, group.Members, 1)
	assert.Equal(t, userID, group.Members[0].Value)

	userUpdate, err := json.Marshal(activeUserInput("bulk-user", "bulk-user", "bulk-updated@example.test"))
	require.NoError(t, err)
	userPatch := []byte(`{"schemas":["` + PatchSchema + `"],"Operations":[{"op":"replace","path":"displayName","value":"Bulk Patched"}]}`)
	groupUpdate, err := json.Marshal(GroupInput{Schemas: []string{GroupSchema}, ExternalID: "bulk-group", DisplayName: "Bulk Group Updated", Members: []SCIMGroupMember{{Value: userID}}})
	require.NoError(t, err)
	groupPatch := []byte(`{"schemas":["` + PatchSchema + `"],"Operations":[{"op":"replace","path":"displayName","value":"Bulk Group Patched"}]}`)
	response, err = env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{
		{Method: http.MethodPut, Path: "/Users/" + userID, Version: weakETag(1), Data: userUpdate},
		{Method: http.MethodPatch, Path: "/Users/" + userID, Version: weakETag(2), Data: userPatch},
		{Method: http.MethodPut, Path: "/Groups/" + groupID, Version: weakETag(1), Data: groupUpdate},
		{Method: http.MethodPatch, Path: "/Groups/" + groupID, Version: weakETag(2), Data: groupPatch},
		{Method: http.MethodDelete, Path: "/Groups/" + groupID, Version: weakETag(3)},
		{Method: http.MethodDelete, Path: "/Users/" + userID, Version: weakETag(3)},
	}})
	require.NoError(t, err)
	require.Len(t, response.Operations, 6)
	assert.Equal(t, []string{"200", "200", "200", "200", "204", "204"}, []string{
		response.Operations[0].Status, response.Operations[1].Status, response.Operations[2].Status,
		response.Operations[3].Status, response.Operations[4].Status, response.Operations[5].Status,
	})

	response, err = env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, FailOnErrors: 1, Operations: []BulkOperation{
		{Method: http.MethodPost, BulkID: "bad", Path: "/Users", Data: []byte(`{"schemas":[]}`)},
		{Method: http.MethodPost, BulkID: "not-run", Path: "/Users", Data: userData},
	}})
	require.NoError(t, err)
	require.Len(t, response.Operations, 1)
	assert.Equal(t, "400", response.Operations[0].Status)

	forwardData, err := json.Marshal(GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Forward", Members: []SCIMGroupMember{{Value: "bulkId:future"}}})
	require.NoError(t, err)
	response, err = env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{{Method: http.MethodPost, BulkID: "forward", Path: "/Groups", Data: forwardData}}})
	require.NoError(t, err)
	assert.Equal(t, "400", response.Operations[0].Status)

	_, err = env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{"bad"}, Operations: []BulkOperation{{}}})
	assert.ErrorIs(t, err, ErrInvalidSCIM)
	_, err = env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: make([]BulkOperation, maxBulkOps+1)})
	assert.ErrorIs(t, err, ErrTooMany)
}

func TestBulkPathAndReferenceValidation(t *testing.T) {
	resourceType, id, err := bulkPath("/scim/v2/Users/" + wireID("user"))
	require.NoError(t, err)
	assert.Equal(t, ResourceUser, resourceType)
	assert.Equal(t, wireID("user"), id)
	for _, path := range []string{"", "/Unknown", "/Users/a/b", "/Users?id=x", "https://example.test/Users"} {
		_, _, err := bulkPath(path)
		assert.ErrorIs(t, err, ErrInvalidSCIM, path)
	}
	value, err := resolveBulkValue(map[string]any{"members": []any{map[string]any{"value": "bulkId:user"}}}, map[string]string{"user": wireID("principal")})
	require.NoError(t, err)
	assert.Equal(t, wireID("principal"), value.(map[string]any)["members"].([]any)[0].(map[string]any)["value"])
	_, err = resolveBulkValue("bulkId:missing", map[string]string{})
	assert.ErrorIs(t, err, ErrInvalidPath)
}

func TestBulkRejectsInvalidOperationShapes(t *testing.T) {
	env := newProvisioningEnvironment(t)
	validUser, err := json.Marshal(activeUserInput("bulk-shape", "bulk-shape", "bulk-shape@example.test"))
	require.NoError(t, err)
	tests := []BulkOperation{
		{Method: http.MethodPost, Path: "/Users", Data: validUser},
		{Method: http.MethodPost, BulkID: strings.Repeat("x", 129), Path: "/Users", Data: validUser},
		{Method: http.MethodPut, BulkID: "not-allowed", Path: "/Users/" + wireID("user"), Data: validUser},
		{Method: http.MethodPut, Path: "/Users/not-an-id", Data: validUser},
		{Method: http.MethodDelete, Path: "/Users/" + wireID("user"), Data: []byte(`{}`)},
		{Method: "OPTIONS", Path: "/Users/" + wireID("user")},
	}
	for _, operation := range tests {
		response, err := env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{operation}})
		require.NoError(t, err)
		require.Len(t, response.Operations, 1)
		assert.Equal(t, "400", response.Operations[0].Status)
	}

	response, err := env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{
		{Method: http.MethodPost, BulkID: "duplicate", Path: "/Users", Data: validUser},
		{Method: http.MethodPost, BulkID: "duplicate", Path: "/Users", Data: validUser},
	}})
	require.NoError(t, err)
	assert.Equal(t, "201", response.Operations[0].Status)
	assert.Equal(t, "400", response.Operations[1].Status)
}

func pathID(location string) string {
	for index := len(location) - 1; index >= 0; index-- {
		if location[index] == '/' {
			return location[index+1:]
		}
	}
	return location
}

// TestBulkRejectsMalformedOperationPayloads covers every decode guard on the
// bulk path with data an external SCIM provider could actually send. Each
// operation is well formed up to the payload, so a guard that stopped
// rejecting would let an unknown attribute, a body on a DELETE, an unparseable
// document or a bad precondition through to the service instead of returning a
// protocol error for that one operation.
func TestBulkRejectsMalformedOperationPayloads(t *testing.T) {
	env := newProvisioningEnvironment(t)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("bulk-malformed", "bulk-malformed", "bulk-malformed@example.test"))
	require.NoError(t, err)
	group, err := env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Bulk Malformed"})
	require.NoError(t, err)
	validUser, err := json.Marshal(activeUserInput("bulk-version", "bulk-version", "bulk-version@example.test"))
	require.NoError(t, err)

	strayUser := json.RawMessage(`{"schemas":["` + UserSchema + `"],"userName":"stray","unexpected":true}`)
	strayGroup := json.RawMessage(`{"schemas":["` + GroupSchema + `"],"displayName":"Stray","unexpected":true}`)
	strayPatch := json.RawMessage(`{"schemas":["` + PatchSchema + `"],"Operations":[],"unexpected":true}`)
	for _, test := range []struct {
		name      string
		operation BulkOperation
		status    string
	}{
		{"user post unknown attribute", BulkOperation{Method: http.MethodPost, BulkID: "stray", Path: "/Users", Data: strayUser}, "400"},
		{"user put unknown attribute", BulkOperation{Method: http.MethodPut, Path: "/Users/" + user.ID, Data: strayUser}, "400"},
		{"user patch unknown attribute", BulkOperation{Method: http.MethodPatch, Path: "/Users/" + user.ID, Data: strayPatch}, "400"},
		{"group post unknown attribute", BulkOperation{Method: http.MethodPost, BulkID: "stray", Path: "/Groups", Data: strayGroup}, "400"},
		{"group put unknown attribute", BulkOperation{Method: http.MethodPut, Path: "/Groups/" + group.ID, Data: strayGroup}, "400"},
		{"group patch unknown attribute", BulkOperation{Method: http.MethodPatch, Path: "/Groups/" + group.ID, Data: strayPatch}, "400"},
		{"group delete carrying a body", BulkOperation{Method: http.MethodDelete, Path: "/Groups/" + group.ID, Data: json.RawMessage(`{}`)}, "400"},
		{"unknown collection", BulkOperation{Method: http.MethodPost, BulkID: "stray", Path: "/Unknown"}, "400"},
		{"undecodable document", BulkOperation{Method: http.MethodPut, Path: "/Users/" + user.ID, Data: json.RawMessage(`{`)}, "400"},
		{"malformed precondition", BulkOperation{Method: http.MethodPut, Path: "/Users/" + user.ID, Version: "not-an-etag", Data: validUser}, "412"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{test.operation}})
			require.NoError(t, err)
			require.Len(t, response.Operations, 1)
			assert.Equal(t, test.status, response.Operations[0].Status)
		})
	}

	// None of the rejected operations may have touched the stored resources.
	stored, err := env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
	require.NoError(t, err)
	assert.Equal(t, user, stored)
	storedGroup, err := env.service.GetGroup(t.Context(), env.auth, parseGUID(group.ID))
	require.NoError(t, err)
	assert.Equal(t, group, storedGroup)

	// A path segment longer than the identifier limit never reaches the store.
	_, _, err = bulkPath("/Users/" + strings.Repeat("9", 129))
	assert.ErrorIs(t, err, ErrInvalidSCIM)
	_, _, err = bulkPath("/Users/%zz")
	assert.ErrorIs(t, err, ErrInvalidSCIM)
}

// TestBulkReportsPerOperationFailuresWithoutAbortingTheBatch proves a failing
// sub-operation is reported as its own protocol error and the batch keeps
// going, rather than the failure escaping as a request-level error or
// silently truncating the remaining operations.
func TestBulkReportsPerOperationFailuresWithoutAbortingTheBatch(t *testing.T) {
	env := newProvisioningEnvironment(t)
	missing := wireID("bulk-missing-resource")
	groupBody, err := json.Marshal(GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Ghost"})
	require.NoError(t, err)
	patchBody := json.RawMessage(`{"schemas":["` + PatchSchema + `"],"Operations":[{"op":"replace","path":"displayName","value":"Ghost"}]}`)
	namelessGroup := json.RawMessage(`{"schemas":["` + GroupSchema + `"],"displayName":"   "}`)
	survivor, err := json.Marshal(activeUserInput("bulk-survivor", "bulk-survivor", "bulk-survivor@example.test"))
	require.NoError(t, err)

	response, err := env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{
		{Method: http.MethodDelete, Path: "/Users/" + missing},
		{Method: http.MethodDelete, Path: "/Groups/" + missing},
		{Method: http.MethodPut, Path: "/Groups/" + missing, Data: groupBody},
		{Method: http.MethodPatch, Path: "/Groups/" + missing, Data: patchBody},
		{Method: http.MethodPost, BulkID: "nameless", Path: "/Groups", Data: namelessGroup},
		{Method: http.MethodPost, BulkID: "survivor", Path: "/Users", Data: survivor},
	}})
	require.NoError(t, err)
	require.Len(t, response.Operations, 6, "a failing operation must not truncate the batch")
	statuses := make([]string, 0, len(response.Operations))
	for _, operation := range response.Operations {
		statuses = append(statuses, operation.Status)
	}
	assert.Equal(t, []string{"404", "404", "404", "404", "400", "201"}, statuses)
	assert.NotEmpty(t, response.Operations[5].Location, "the operation after the failures must still have been applied")
}
