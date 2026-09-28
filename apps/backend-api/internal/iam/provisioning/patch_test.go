package provisioning

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserPatchWithoutETagPreservesConcurrentDisjointChanges(t *testing.T) {
	for _, prefix := range []string{"", "patch_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			env := newProvisioningEnvironmentWithPrefix(t, prefix)
			user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("patch-concurrent", "patch-concurrent", "original@example.test"))
			require.NoError(t, err)
			for range 12 {
				input := activeUserInput("patch-concurrent", "patch-concurrent", "original@example.test")
				input.DisplayName = "Original"
				_, err := env.service.ReplaceUser(t.Context(), env.auth, parseGUID(user.ID), input, 0)
				require.NoError(t, err)
				start, results := make(chan struct{}), make(chan error, 2)
				var workers sync.WaitGroup
				for _, operation := range []PatchOperation{
					{Op: "replace", Path: "displayName", Value: json.RawMessage(`"Changed"`)},
					{Op: "replace", Path: "emails", Value: json.RawMessage(`[{"value":"changed@example.test","primary":true}]`)},
				} {
					workers.Add(1)
					go func(operation PatchOperation) {
						defer workers.Done()
						<-start
						_, err := env.service.PatchUser(t.Context(), env.auth, parseGUID(user.ID), PatchRequest{Schemas: []string{PatchSchema}, Operations: []PatchOperation{operation}}, 0)
						results <- err
					}(operation)
				}
				close(start)
				workers.Wait()
				close(results)
				for err := range results {
					require.NoError(t, err)
				}
				current, err := env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
				require.NoError(t, err)
				assert.Equal(t, "Changed", current.DisplayName)
				require.Len(t, current.Emails, 1)
				assert.Equal(t, "changed@example.test", current.Emails[0].Value)
				assert.Equal(t, user.UserName, current.UserName)
			}
		})
	}
}

func TestUserPatchVersionFailureAndAuditFailureDoNotMutateIdentity(t *testing.T) {
	env := newProvisioningEnvironment(t)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("patch-rollback", "patch-rollback", "original@example.test"))
	require.NoError(t, err)
	patch := PatchRequest{Schemas: []string{PatchSchema}, Operations: []PatchOperation{{Op: "replace", Path: "displayName", Value: json.RawMessage(`"Changed"`)}}}
	_, err = env.service.PatchUser(t.Context(), env.auth, parseGUID(user.ID), patch, 2)
	assert.ErrorIs(t, err, ErrResourceVersion)
	env.auditErr = errors.New("audit storage unavailable")
	_, err = env.service.PatchUser(t.Context(), env.auth, parseGUID(user.ID), patch, 1)
	assert.ErrorIs(t, err, env.auditErr)
	env.auditErr = nil
	current, err := env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
	require.NoError(t, err)
	assert.Equal(t, user, current)
}

func TestUserPatchValidationAndApplication(t *testing.T) {
	active := true
	input := UserInput{Schemas: []string{UserSchema}, UserName: "alice", DisplayName: "Alice", Active: &active, Emails: []UserEmail{{Value: "old@example.test", Primary: true}}}
	operations := []PatchOperation{
		{Op: "replace", Value: json.RawMessage(`{"userName":"alice-one","displayName":"Alice One","name":{"formatted":"Alice Object"},"active":true,"externalId":"external-1","emails":[{"value":"object@example.test","primary":true}]}`)},
		{Op: "replace", Path: "userName", Value: json.RawMessage(`"alice"`)},
		{Op: "replace", Path: UserSchema + ":name.formatted", Value: json.RawMessage(`"Alice Formatted"`)},
		{Op: "replace", Path: "active", Value: json.RawMessage(`false`)},
		{Op: "replace", Path: "emails", Value: json.RawMessage(`[{"value":"new@example.test","primary":true}]`)},
		{Op: "add", Path: "emails", Value: json.RawMessage(`{"value":"second@example.test"}`)},
		{Op: "remove", Path: "displayName"},
		{Op: "remove", Path: "name.formatted"},
		{Op: "remove", Path: "externalId"},
		{Op: "add", Path: "externalId", Value: json.RawMessage(`"external-2"`)},
	}
	for _, operation := range operations {
		require.NoError(t, applyUserPatch(&input, operation))
	}
	assert.Empty(t, input.DisplayName)
	assert.Empty(t, input.Name.Formatted)
	assert.False(t, *input.Active)
	assert.Equal(t, "external-2", input.ExternalID)
	require.Len(t, input.Emails, 2)
	require.NoError(t, applyUserPatch(&input, PatchOperation{Op: "remove", Path: "emails"}))
	assert.Empty(t, input.Emails)

	for _, operation := range []PatchOperation{
		{Op: "replace", Path: "active", Value: json.RawMessage(`"false"`)},
		{Op: "replace", Path: GroupSchema + ":displayName", Value: json.RawMessage(`"wrong schema"`)},
		{Op: "replace", Path: `emails[value eq "old@example.test"]`, Value: json.RawMessage(`[]`)},
		{Op: "replace", Path: "[", Value: json.RawMessage(`"x"`)},
		{Op: "replace", Path: "emails", Value: json.RawMessage(`true`)},
		{Op: "replace", Value: json.RawMessage(`{"unknown":true}`)},
		{Op: "remove"},
		{Op: "copy", Path: "displayName", Value: json.RawMessage(`"x"`)},
	} {
		assert.ErrorIs(t, applyUserPatch(&input, operation), ErrInvalidPath)
	}
	assert.ErrorIs(t, applyUserPatch(&input, PatchOperation{Op: "remove", Path: "userName"}), identity.ErrImmutableProvisionedLogin)
}

func TestGroupPatchMemberOperations(t *testing.T) {
	input := GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Team", Members: []SCIMGroupMember{{Value: wireID("one")}, {Value: wireID("two")}}}
	require.NoError(t, applyGroupPatch(&input, PatchOperation{Op: "add", Value: json.RawMessage(`{"displayName":"Team Added","externalId":"external","members":[{"value":"` + wireID("zero") + `"}]}`)}))
	assert.Equal(t, "Team Added", input.DisplayName)
	require.Len(t, input.Members, 3)
	require.NoError(t, applyGroupPatch(&input, PatchOperation{Op: "replace", Path: "displayName", Value: json.RawMessage(`"Team"`)}))
	require.NoError(t, applyGroupPatch(&input, PatchOperation{Op: "replace", Path: "externalId", Value: json.RawMessage(`"external-2"`)}))
	require.NoError(t, applyGroupPatch(&input, PatchOperation{Op: "remove", Path: "externalId"}))
	require.NoError(t, applyGroupPatch(&input, PatchOperation{Op: "add", Path: "members", Value: json.RawMessage(`{"value":"` + wireID("three") + `"}`)}))
	require.NoError(t, applyGroupPatch(&input, PatchOperation{Op: "remove", Path: `members[value eq "` + wireID("two") + `"]`}))
	require.Len(t, input.Members, 3)
	require.NoError(t, applyGroupPatch(&input, PatchOperation{Op: "replace", Path: "members", Value: json.RawMessage(`[{"value":"` + wireID("one") + `"}]`)}))
	require.Len(t, input.Members, 1)
	require.NoError(t, applyGroupPatch(&input, PatchOperation{Op: "remove", Path: "members"}))
	assert.Empty(t, input.Members)

	for _, operation := range []PatchOperation{
		{Op: "remove", Path: "displayName"},
		{Op: "replace", Path: `members[value eq "` + wireID("one") + `"]`, Value: json.RawMessage(`{"value":"` + wireID("two") + `"}`)},
		{Op: "remove", Path: `members[value co "` + wireID("one") + `"]`},
		{Op: "replace", Path: "members", Value: json.RawMessage(`true`)},
		{Op: "replace", Path: "unknown", Value: json.RawMessage(`"x"`)},
		{Op: "replace", Path: UserSchema + ":displayName", Value: json.RawMessage(`"x"`)},
	} {
		assert.ErrorIs(t, applyGroupPatch(&input, operation), ErrInvalidPath)
	}
}

func TestPatchEnvelopeLimits(t *testing.T) {
	assert.True(t, validPatch(PatchRequest{Schemas: []string{PatchSchema}, Operations: []PatchOperation{{Op: "remove", Path: "members"}}}))
	assert.False(t, validPatch(PatchRequest{Schemas: []string{"wrong"}, Operations: []PatchOperation{{}}}))
	assert.False(t, validPatch(PatchRequest{Schemas: []string{PatchSchema}}))
	assert.False(t, validPatch(PatchRequest{Schemas: []string{PatchSchema}, Operations: make([]PatchOperation, maxPatchOps+1)}))
}
