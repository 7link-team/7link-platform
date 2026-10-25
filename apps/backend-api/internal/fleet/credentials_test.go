package fleet

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMachineCredentialsIAMOwnershipAndCurrentAuthority(t *testing.T) {
	m, _, ctx, _, _ := enrollmentHarness(t)
	m.enrollmentIAM.ListCredentials = func(_ context.Context, tenant, principal guid.ID) ([]identity.ServiceAccountCredential, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(101), principal)
		return []identity.ServiceAccountCredential{{ID: 303, ServiceAccountID: 101, Name: "Machine credential"}}, nil
	}
	out, e := m.machineCredentials(ctx, 1)
	require.NoError(t, e)
	require.Len(t, out.Items, 1)
	require.True(t, out.RotationAvailable)
	require.False(t, out.RevocationAvailable)
	original := m.enrollmentIAM.ListCredentials
	m.enrollmentIAM.ListCredentials = func(ctx context.Context, tenant, principal guid.ID) ([]identity.ServiceAccountCredential, error) {
		rows, err := original(ctx, tenant, principal)
		m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, nil
		}
		return rows, err
	}
	out, e = m.machineCredentials(ctx, 1)
	require.ErrorIs(t, e, ErrMachineForbidden)
	require.Empty(t, out.Items)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.enrollmentIAM.ListCredentials = func(context.Context, guid.ID, guid.ID) ([]identity.ServiceAccountCredential, error) {
		return []identity.ServiceAccountCredential{{ID: 303, ServiceAccountID: 999}}, nil
	}
	out, e = m.machineCredentials(ctx, 1)
	require.ErrorIs(t, e, ErrUnavailable)
	require.Empty(t, out.Items)
	m.enrollmentIAM.ListCredentials = func(context.Context, guid.ID, guid.ID) ([]identity.ServiceAccountCredential, error) {
		return nil, ErrUnavailable
	}
	_, e = m.machineCredentials(ctx, 1)
	require.ErrorIs(t, e, ErrUnavailable)
	m.enrollmentIAM.ListCredentials = nil
	_, e = m.machineCredentials(ctx, 1)
	require.ErrorIs(t, e, ErrUnavailable)
	_, e = (*Module)(nil).machineCredentials(ctx, 1)
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestMachineCredentialsHTTPCurrentTenantAndUnavailable(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	m.enrollmentIAM.ListCredentials = func(context.Context, guid.ID, guid.ID) ([]identity.ServiceAccountCredential, error) {
		return []identity.ServiceAccountCredential{{ID: 303, ServiceAccountID: 101, Name: "Metadata only"}}, nil
	}
	router := fleetBusinessRouter(t, m)
	response := fleetHTTPRequest(router, http.MethodGet, "/machines/1/credentials", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Metadata only")
	require.NotContains(t, response.Body.String(), "client_secret")
	require.Contains(t, response.Body.String(), `"rotation_available":true`)
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/999/credentials", "", nil)
	require.Equal(t, http.StatusNotFound, response.Code)
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/9999999999999999999/credentials", "", nil)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	m.enrollmentIAM.ListCredentials = nil
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/1/credentials", "", nil)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
}
