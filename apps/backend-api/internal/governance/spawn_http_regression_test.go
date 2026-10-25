package governance

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
)

func TestSpawnHTTPInvalidSelectorsAndUnavailableWriterFailClosed(t *testing.T) {
	for _, fault := range []string{"tenant", "grant", "version", "writer"} {
		t.Run(fault, func(t *testing.T) {
			m, _, ctx, _, _ := spawnGrantHarness(t)
			ref := SpawnRefInput{TenantID: "1", ID: "77"}
			etag := `"1"`
			status := http.StatusBadRequest
			switch fault {
			case "tenant":
				ref.TenantID = "bad"
			case "grant":
				ref.ID = "bad"
			case "version":
				etag = "1"
			case "writer":
				m.runtime = nil
				status = http.StatusServiceUnavailable
			}
			assertStatus := func(err error) {
				t.Helper()
				var response huma.StatusError
				require.ErrorAs(t, err, &response)
				require.Equal(t, status, response.GetStatus())
			}
			_, err := m.updateSpawnHTTP(ctx, &spawnUpdateInput{SpawnRefInput: ref, IfMatch: etag})
			assertStatus(err)
			_, err = m.revokeSpawnHTTP(ctx, &spawnRevokeInput{SpawnRefInput: ref, IfMatch: etag, Key: "key"})
			assertStatus(err)
			if fault == "version" {
				return
			}
			_, err = m.getSpawnHTTP(ctx, &ref)
			assertStatus(err)
			_, err = m.membersSpawnHTTP(ctx, &ref)
			assertStatus(err)
			if fault == "grant" {
				return
			}
			_, err = m.listSpawnHTTP(ctx, &spawnListInput{TenantID: ref.TenantID, Limit: 50})
			assertStatus(err)
			_, err = m.createSpawnHTTP(ctx, &spawnCreateInput{TenantID: ref.TenantID, Key: "key"})
			assertStatus(err)
		})
	}
}

func TestSpawnHTTPErrorCodesAndCurrentReadFailures(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		err    error
		status int
	}{
		{ErrSpawnScope, 422}, {ErrSpawnQuota, 409}, {ErrSpawnBudget, 409}, {ErrSpawnActive, 409},
	} {
		err := spawnHTTPError(ctx, tc.err)
		var response huma.StatusError
		require.ErrorAs(t, err, &response)
		require.Equal(t, tc.status, response.GetStatus())
	}
	m, db, ctx, _, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	_, err = m.getSpawnHTTP(ctx, &SpawnRefInput{TenantID: "1", ID: "999"})
	require.Error(t, err)
	m.delegations.members = delegationMembers{false}
	_, err = m.listSpawnHTTP(ctx, &spawnListInput{TenantID: "1", Limit: 50})
	require.Error(t, err)
	_, err = m.getSpawnHTTP(ctx, &SpawnRefInput{TenantID: "1", ID: grant.ID.String()})
	require.Error(t, err)
	_, err = m.membersSpawnHTTP(ctx, &SpawnRefInput{TenantID: "1", ID: grant.ID.String()})
	require.Error(t, err)
}
