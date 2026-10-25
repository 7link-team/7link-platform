package fleet

import (
	"context"
	"net/url"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
)

// fleetIAMAdapter binds consumer functions during Provide; method lookup is
// deferred until use because IAM services initialize after registry wiring.
type fleetIAMAdapter struct {
	enabled  bool
	identity identity.EnrollmentAccounts
	oauth    oauth.EnrollmentTokens
}

func (*fleetIAMAdapter) ModuleName() string           { return IAMBridgeName }
func (m *fleetIAMAdapter) Enabled() bool              { return m.enabled }
func (*fleetIAMAdapter) ModuleDependencies() []string { return []string{identity.Name, oauth.Name} }
func (m *fleetIAMAdapter) ProvideServices(s *module.Services) error {
	return module.Provide[EnrollmentIAM](s, EnrollmentIAM{
		Token: func(ctx context.Context, form url.Values, authorization string) (oauth.TokenResponse, error) {
			if m.oauth == nil || !m.oauth.EnrollmentAvailable() {
				return oauth.TokenResponse{}, oauth.ErrUnavailable
			}
			return m.oauth.Token(ctx, form, authorization)
		},
		FindAccount: func(ctx context.Context, tenant guid.ID, login string) (identity.ServiceAccount, bool, error) {
			if m.identity == nil || !m.identity.EnrollmentAvailable() {
				return identity.ServiceAccount{}, false, identity.ErrUnavailable
			}
			rows, _, err := m.identity.ListServiceAccounts(ctx, tenant, login, 200, 0)
			if err != nil {
				return identity.ServiceAccount{}, false, err
			}
			for _, row := range rows {
				if row.LoginName == login {
					return row, true, nil
				}
			}
			return identity.ServiceAccount{}, false, nil
		},
		ListCredentials: func(ctx context.Context, tenant, id guid.ID) ([]identity.ServiceAccountCredential, error) {
			if m.identity == nil || !m.identity.EnrollmentAvailable() {
				return nil, identity.ErrUnavailable
			}
			return m.identity.ListServiceAccountCredentials(ctx, tenant, id)
		},
		StartDevice: func(ctx context.Context, client, scope string) (oauth.DeviceAuthorization, error) {
			if m.oauth == nil || !m.oauth.EnrollmentAvailable() {
				return oauth.DeviceAuthorization{}, oauth.ErrUnavailable
			}
			return m.oauth.StartDeviceAuthorization(ctx, client, scope)
		},
		LookupDevice: func(ctx context.Context, tenant guid.ID, code string) (oauth.DeviceRequest, error) {
			if m.oauth == nil || !m.oauth.EnrollmentAvailable() {
				return oauth.DeviceRequest{}, oauth.ErrUnavailable
			}
			return m.oauth.LookupDeviceRequest(ctx, tenant, code)
		},
		DecideDevice: func(ctx context.Context, tenant guid.ID, code string, actor guid.ID, approve bool, at time.Time, acr int, amr string) error {
			if m.oauth == nil || !m.oauth.EnrollmentAvailable() {
				return oauth.ErrUnavailable
			}
			return m.oauth.DecideDeviceRequest(ctx, tenant, code, actor, approve, at, acr, amr)
		},
		CreateAccount: func(ctx context.Context, tenant guid.ID, login, name, description string, expiry *time.Time) (identity.ServiceAccount, error) {
			if m.identity == nil || !m.identity.EnrollmentAvailable() {
				return identity.ServiceAccount{}, identity.ErrUnavailable
			}
			return m.identity.CreateServiceAccount(ctx, tenant, login, name, description, expiry)
		},
		GetAccount: func(ctx context.Context, tenant, id guid.ID) (identity.ServiceAccount, error) {
			if m.identity == nil || !m.identity.EnrollmentAvailable() {
				return identity.ServiceAccount{}, identity.ErrUnavailable
			}
			return m.identity.GetServiceAccount(ctx, tenant, id)
		},
		DeleteAccount: func(ctx context.Context, tenant, id guid.ID, version int64) error {
			if m.identity == nil || !m.identity.EnrollmentAvailable() {
				return identity.ErrUnavailable
			}
			return m.identity.DeleteServiceAccount(ctx, tenant, id, version)
		},
		IssueCredential: func(ctx context.Context, tenant, id guid.ID, name string, scopes []string, expiry *time.Time) (identity.ServiceAccountCredentialSecret, error) {
			if m.identity == nil || !m.identity.EnrollmentAvailable() {
				return identity.ServiceAccountCredentialSecret{}, identity.ErrUnavailable
			}
			return m.identity.CreateServiceAccountCredential(ctx, tenant, id, name, scopes, expiry)
		},
		RevokeCredential: func(ctx context.Context, tenant, id, credential guid.ID) error {
			if m.identity == nil || !m.identity.EnrollmentAvailable() {
				return identity.ErrUnavailable
			}
			return m.identity.RevokeServiceAccountCredential(ctx, tenant, id, credential)
		},
	})
}

var (
	_ module.ServiceProvider = (*fleetIAMAdapter)(nil)
	_ module.Depender        = (*fleetIAMAdapter)(nil)
	_ module.Enabler         = (*fleetIAMAdapter)(nil)
)

// NewIAMBridge binds enrollment using provider-owned capabilities.
func NewIAMBridge(enabled bool) module.Module { return &fleetIAMAdapter{enabled: enabled} }
func (m *fleetIAMAdapter) InjectServices(s *module.Services) error {
	var err error
	if m.identity, err = module.Resolve[identity.EnrollmentAccounts](s); err != nil {
		return err
	}
	m.oauth, err = module.Resolve[oauth.EnrollmentTokens](s)
	return err
}

var _ module.ServiceInjector = (*fleetIAMAdapter)(nil)
