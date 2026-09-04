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
