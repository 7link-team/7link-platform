package fleet

import (
	"net/url"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
)

func TestFleetIAMAdapterProvidesBeforeIAMInitialization(t *testing.T) {
	adapter := &fleetIAMAdapter{enabled: true, identity: &identity.Module{}, oauth: &oauth.Module{}}
	require.Equal(t, IAMBridgeName, adapter.ModuleName())
	require.Equal(t, []string{identity.Name, oauth.Name}, adapter.ModuleDependencies())
	require.True(t, adapter.Enabled())
	adapter.enabled = false
	require.False(t, adapter.Enabled())
	adapter.enabled = true

	services := module.NewServices()
	require.NoError(t, adapter.ProvideServices(services), "providing closures must not require initialized IAM services")
	require.ErrorIs(t, adapter.ProvideServices(services), module.ErrServiceDuplicate)
	require.NoError(t, services.Seal())
	port, err := module.Resolve[EnrollmentIAM](services)
	require.NoError(t, err)
	require.True(t, port.Bound(), "the consumer must receive every enrollment capability")
	require.ErrorIs(t, adapter.ProvideServices(services), module.ErrServicesSealed)

	for _, tc := range []struct {
		name string
		want error
		call func(*testing.T) error
	}{
		{"StartDevice", oauth.ErrUnavailable, func(t *testing.T) error {
			value, err := port.StartDevice(t.Context(), "20", "")
			require.Zero(t, value)
			return err
		}},
		{"LookupDevice", oauth.ErrUnavailable, func(t *testing.T) error {
			value, err := port.LookupDevice(t.Context(), 7, "ABCD-EFGH")
			require.Zero(t, value)
			return err
		}},
		{"DecideDevice", oauth.ErrUnavailable, func(t *testing.T) error {
			return port.DecideDevice(t.Context(), 7, "ABCD-EFGH", 101, true, time.Now(), 0, "")
		}},
		{"Token", oauth.ErrUnavailable, func(t *testing.T) error {
			value, err := port.Token(t.Context(), url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, "")
			require.Zero(t, value)
			return err
		}},
		{"CreateAccount", identity.ErrUnavailable, func(t *testing.T) error {
			value, err := port.CreateAccount(t.Context(), 7, "machine-1", "runner", "", nil)
			require.Zero(t, value)
			return err
		}},
		{"GetAccount", identity.ErrUnavailable, func(t *testing.T) error {
			value, err := port.GetAccount(t.Context(), 7, 202)
			require.Zero(t, value)
			return err
		}},
		{"FindAccount", identity.ErrUnavailable, func(t *testing.T) error {
			value, found, err := port.FindAccount(t.Context(), 7, "machine-1")
			require.Zero(t, value)
			require.False(t, found)
			return err
		}},
		{"DeleteAccount", identity.ErrUnavailable, func(t *testing.T) error {
			return port.DeleteAccount(t.Context(), 7, 202, 1)
		}},
		{"IssueCredential", identity.ErrUnavailable, func(t *testing.T) error {
			value, err := port.IssueCredential(t.Context(), 7, 202, "runner-1", nil, nil)
			require.Zero(t, value)
			return err
		}},
		{"RevokeCredential", identity.ErrUnavailable, func(t *testing.T) error {
			return port.RevokeCredential(t.Context(), 7, 202, 303)
		}},
		{"ListCredentials", identity.ErrUnavailable, func(t *testing.T) error {
			value, err := port.ListCredentials(t.Context(), 7, 202)
			require.Nil(t, value)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.call(t), tc.want, "an uninitialized IAM service must fail closed")
		})
	}
}
